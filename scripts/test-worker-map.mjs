import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { once } from 'node:events';
import { Script } from 'node:vm';
import { spawnSync } from 'node:child_process';
import { parseArgs, databaseEnvironment, renderHTML, serveMapFile, sectorColor } from './worker-map.mjs';

test('bounded numeric options and explicit output', () => {
  assert.deepEqual(parseArgs([]), { hours: 6, limit: 2000, output: '.local/worker-map.html' });
  assert.deepEqual(parseArgs(['--hours', '0.5', '--limit', '10', '--output', 'map.html']), { hours: 0.5, limit: 10, output: 'map.html' });
  assert.equal(parseArgs(['--serve', '--port', '8089']).port, 8089);
  assert.equal(parseArgs(['--serve']).serve, true);
  for (const args of [['--port', '8089'], ['--serve', '--port', '0'], ['--hours', 'NaN'], ['--hours', '0'], ['--hours', '169'], ['--hours', '1;DROP TABLE x'], ['--limit', '2.5'], ['--limit', '5001'], ['--output'], ['--unknown', 'x']]) {
    assert.throws(() => parseArgs(args));
  }
});

test('sector colors interpolate continuously by slow passage share', () => {
  for (const [slow, color] of [[0, '#ffffcc'], [5, '#fed797'], [10, '#fdae61'],
    [15, '#ea6f68'], [20, '#d7306f'], [25, '#91186f'],
    [30, '#4b006e'], [31, '#4b006e'], [50, '#4b006e'], [100, '#4b006e']]) {
    assert.equal(sectorColor({ pasajes: 100, pasajes_lentos: slow }), color);
  }
  // Absolute counts and incomplete passages do not determine the color.
  assert.equal(sectorColor({ pasajes: 1, pasajes_lentos: 1 }), '#4b006e');
  assert.equal(sectorColor({ pasajes: 4, pasajes_lentos: 1, incompletos: 100 }), '#91186f');
  assert.equal(sectorColor({ pasajes: 3, pasajes_lentos: 0 }), '#ffffcc');
  assert.equal(sectorColor({ pasajes: 0, pasajes_lentos: 0, incompletos: 10 }), '#808080');
});

test('PostgreSQL URI is passed through environment, with decoded credentials', () => {
  const env = databaseEnvironment('postgresql://test%40user:fake%3Apass@localhost:5433/test%20db?sslmode=require');
  assert.equal(env.PGHOST, 'localhost');
  assert.equal(env.PGPORT, '5433');
  assert.equal(env.PGUSER, 'test@user');
  assert.equal(env.PGPASSWORD, 'fake:pass');
  assert.equal(env.PGDATABASE, 'test db');
  assert.equal(env.PGSSLMODE, 'require');
  assert.match(env.PGOPTIONS, /default_transaction_read_only=on/);
});

test('empty snapshot renders with valid browser JavaScript', () => {
  const data = {
    generated_at: '2026-10-21T22:00:00Z', hours: 6, limit: 2000,
    status: { capture: null, processors: [], states: 0, open_sequences: 0 },
    events: { type: 'FeatureCollection', features: [] },
    sectors: { type: 'FeatureCollection', features: [] },
  };
  const html = renderHTML(data);
  const payload = html.match(/<script id="data" type="application\/json">([\s\S]*?)<\/script>/)[1];
  assert.deepEqual(JSON.parse(payload), data);
  new Script(html.match(/<script id="map-code">([\s\S]*?)<\/script>/)[1]);
  assert.match(html, /mediciones, no cuellos de botella detectados/);
  assert.match(html, /Snapshot estático/);
  assert.match(html, /color según % de pasajes/);
  assert.match(html, /Escala continua: 0%/);
  assert.match(html, /linear-gradient\(to right,#ffffcc,#fdae61,#d7306f,#4b006e\)/);
  assert.ok(!html.includes('data.sectors.features.length=data.limit'));
  assert.ok(html.includes('data.events.features.length=data.limit'));
  assert.match(html, /location.protocol==='file:'/);
  assert.match(html, /strict-origin-when-cross-origin/);
  assert.ok(html.includes('https://tile.openstreetmap.org/{z}/{x}/{y}.png'));
  assert.ok(!html.includes('https://{s}.tile.openstreetmap.org'));
});

test('HTTP server exposes only the snapshot on loopback and preserves referrers', async t => {
  const dir = mkdtempSync(join(tmpdir(), 'worker-map-'));
  const file = join(dir, 'map.html');
  writeFileSync(file, '<html>snapshot</html>');
  const server = serveMapFile(file, 0);
  t.after(async () => {
    await new Promise(resolve => server.close(resolve));
    rmSync(dir, { recursive: true, force: true });
  });
  await once(server, 'listening');
  assert.equal(server.address().address, '127.0.0.1');
  const base = 'http://127.0.0.1:'+server.address().port;
  const response = await fetch(base);
  assert.equal(response.headers.get('referrer-policy'), 'strict-origin-when-cross-origin');
  assert.equal(await response.text(), '<html>snapshot</html>');
  assert.equal((await fetch(base+'/.env')).status, 404);
  assert.equal((await fetch(base, { method: 'POST' })).status, 404);
  writeFileSync(file, '<html>updated</html>');
  assert.equal(await (await fetch(base+'/worker-map.html')).text(), '<html>updated</html>');
});

test('database labels cannot terminate the embedded JSON script', () => {
  const unsafe = '</script><script>alert(1)</script>';
  const html = renderHTML({ label: unsafe });
  assert.ok(!html.includes(unsafe));
  const payload = html.match(/<script id="data" type="application\/json">([\s\S]*?)<\/script>/)[1];
  assert.equal(JSON.parse(payload).label, unsafe);
  assert.ok(!html.includes('.innerHTML'));
});

test('SQL snapshot computes geometry, window, counts and median with session-only fixtures', { skip: !process.env.TEST_DATABASE_URL }, () => {
  const setup = `
    CREATE TEMP TABLE capturas_colectivos (id_captura bigint, recibido_en timestamptz, filas_validas int, filas_insertadas int, cobertura_parcial bool);
    INSERT INTO capturas_colectivos VALUES (1,now()-interval '2 minutes',10,5,false),(2,now(),12,6,false);
    CREATE TEMP TABLE bottlenecks_modelos (nombre text, checkpoint bigint, activado_en timestamptz, ultima_captura_recibida_en timestamptz);
    INSERT INTO bottlenecks_modelos VALUES ('test',1,now(),now());
    CREATE TEMP TABLE bottlenecks_estados_productor (estado jsonb);
    INSERT INTO bottlenecks_estados_productor VALUES ('{"Open":{}}'),('{"Open":null}');
    CREATE TEMP TABLE aglutinaciones_colectivos (
      id_evento bigint,linea text,ramal text,gtfs_direction_id int,primero_observado_en timestamptz,
      ultimo_observado_en timestamptz,cerrado_en timestamptz,motivo_cierre text,observaciones int,
      ultima_distancia_metros float8,ultima_posicion_a geometry,ultima_posicion_b geometry);
    INSERT INTO aglutinaciones_colectivos VALUES
      (1,'132','A',0,now()-interval '1 minute',now(),null,null,2,10,ST_Point(-58.41,-34.6,4326),ST_Point(-58.41,-34.6,4326)),
      (2,'old','B',0,now()-interval '8 hours',now()-interval '7 hours',null,null,1,10,ST_Point(-58,-34,4326),ST_Point(-58,-34,4326));
    CREATE TEMP TABLE bottlenecks_mediciones (trazado bigint,sector int,completa bool,velocidad_kph float8,duracion_ns bigint,entrada_en timestamptz);
    INSERT INTO bottlenecks_mediciones VALUES
      (1,0,true,6,300000000000,now()),(1,0,true,7,299999999999,now()),
      (1,0,true,3,600000000000,now()),(1,0,true,5,360000000000,now()),
      (1,0,false,null,null,now()),
      (1,1,false,null,null,now()),(1,2,true,6,300000000000,now()),
      (1,3,true,5,360000000000,now()),
      (1,4,true,4,450000000000,now()),(1,4,true,5,360000000000,now());
    CREATE TEMP TABLE bottlenecks_sectores (trazado bigint,indice int,inicio_metros float8,fin_metros float8,geom geometry);
    INSERT INTO bottlenecks_sectores SELECT 1,i,i*500,(i+1)*500,
      ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.40 -34.6)',4326) FROM generate_series(0,4) i;
    CREATE TEMP TABLE bottlenecks_trazados (id bigint,linea text,ramal text,direction_id int);
    INSERT INTO bottlenecks_trazados VALUES (1,'132','A',0);
  `;
  const query = readFileSync(new URL('./worker-map.sql', import.meta.url), 'utf8').replaceAll('vialis.', 'pg_temp.');
  const result = spawnSync('psql', ['-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-v', 'hours=6', '-v', 'row_limit=1'], {
    input: setup + query, encoding: 'utf8', timeout: 20000,
    env: { ...databaseEnvironment(process.env.TEST_DATABASE_URL), PGOPTIONS: '-c statement_timeout=15000' },
  });
  assert.equal(result.status, 0, 'session TEMP fixture query failed');
  const data = JSON.parse(result.stdout);
  assert.equal(data.events.features.length, 1);
  assert.equal(data.events.features[0].properties.linea, '132');
  assert.deepEqual(data.events.features[0].geometry.coordinates, [-58.41, -34.6]);
  // The event limit must never truncate the sector layer.
  assert.equal(data.sectors.features.length, 3);
  const sector = data.sectors.features[0];
  assert.equal(sector.geometry.type, 'LineString');
  assert.equal(sector.properties.pasajes, 4);
  assert.equal(sector.properties.pasajes_lentos, 2);
  assert.equal(sector.properties.duracion_mediana_min, 5.5);
  assert.equal(sector.properties.longitud_metros, 500);
  assert.equal(sector.properties.extremo_recorrido, true);
  // Exactly 6 km/h is excluded, even when duration is exactly five minutes.
  assert.equal(data.slow_threshold_kph, 6);
  assert.equal(data.recurrent_min_passages, 2);
  assert.equal(sectorColor(sector.properties), '#4b006e');
  assert.deepEqual(data.sectors.features.map(f => f.properties.sector), [0, 3, 4]);
  const middle = data.sectors.features.find(f => f.properties.sector === 3);
  assert.equal(middle.properties.extremo_recorrido, false);
  assert.equal(middle.properties.pasajes_lentos, 1);
  assert.equal(sectorColor(middle.properties), '#4b006e');
  const last = data.sectors.features.find(f => f.properties.sector === 4);
  assert.equal(last.properties.extremo_recorrido, true);
  assert.equal(last.properties.pasajes_lentos, 2);
  assert.equal(sectorColor(last.properties), '#4b006e');
  assert.equal(sector.properties.incompletos, 1);
  assert.equal(sector.properties.velocidad_mediana_kph, 5.5);
  assert.equal(data.status.processors[0].pending_captures, 1);
  assert.equal(data.status.open_sequences, 1);
});

test('query remains read-only and exports no vehicle identifiers or raw series', () => {
  const sql = readFileSync(new URL('./worker-map.sql', import.meta.url), 'utf8');
  assert.match(sql, /BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY/);
  assert.match(sql, /statement_timeout/);
  assert.doesNotMatch(sql, /\b(?:INSERT|UPDATE|DELETE|TRUNCATE|DROP|ALTER)\b/i);
  assert.doesNotMatch(sql, /id_vehiculo|posiciones_colectivos_raw|soporte/);
});
