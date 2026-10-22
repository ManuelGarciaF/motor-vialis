#!/usr/bin/env node
// Local diagnostic export. Requires Node >= 20, psql and DATABASE_URL.
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { createServer } from 'node:http';

export function parseArgs(args) {
  const options = { hours: 6, limit: 2000, output: '.local/worker-map.html' };
  for (let i = 0; i < args.length; i++) {
    const key = args[i];
    if (key === '--help') return { help: true };
    if (key === '--serve') { options.serve = true; continue; }
    if (!['--hours', '--limit', '--output', '--port'].includes(key) || !args[i + 1]) {
      throw new Error('Uso: node scripts/worker-map.mjs [--hours 6] [--limit 2000] [--output .local/worker-map.html] [--serve] [--port 8088]');
    }
    options[key.slice(2)] = key === '--output' ? args[++i] : Number(args[++i]);
  }
  if (!Number.isFinite(options.hours) || options.hours < 0.1 || options.hours > 168 ||
      !Number.isInteger(options.limit) || options.limit < 1 || options.limit > 5000) {
    throw new Error('--hours debe estar entre 0.1 y 168; --limit debe ser entero entre 1 y 5000.');
  }
  if (options.port !== undefined && (!options.serve || !Number.isInteger(options.port) || options.port < 1024 || options.port > 65535)) {
    throw new Error('--port requiere --serve y un entero entre 1024 y 65535.');
  }
  return options;
}

export function databaseEnvironment(value) {
  const url = new URL(value);
  if (!['postgres:', 'postgresql:'].includes(url.protocol)) throw new Error('DATABASE_URL debe ser una URI PostgreSQL.');
  // libpq does not expand a URI supplied only through PGDATABASE. Split it into
  // environment fields instead of exposing credentials in process arguments.
  const env = { ...process.env, PGHOST: url.hostname.replace(/^\\[|\\]$/g, ''),
    PGPORT: url.port || '5432', PGDATABASE: decodeURIComponent(url.pathname.slice(1)),
    PGCONNECT_TIMEOUT: '5', PGOPTIONS: '-c default_transaction_read_only=on' };
  if (url.username) env.PGUSER = decodeURIComponent(url.username);
  if (url.password) env.PGPASSWORD = decodeURIComponent(url.password);
  for (const [key, variable] of [['sslmode', 'PGSSLMODE'], ['sslrootcert', 'PGSSLROOTCERT'], ['sslcert', 'PGSSLCERT'], ['sslkey', 'PGSSLKEY']]) {
    if (url.searchParams.has(key)) env[variable] = url.searchParams.get(key);
  }
  return env;
}

// Experimental recurrence counts passages, not distinct vehicles or dates.
export function sectorColor(properties, minimumPassages = 2) {
  return properties.pasajes_lentos >= minimumPassages ? '#8e44ad' :
    properties.pasajes_lentos > 0 ? '#d62728' : properties.pasajes > 0 ? '#11865a' : '#c77800';
}

export function renderHTML(data) {
  // Embedded data and all database labels are text, never executable HTML.
  const payload = JSON.stringify(data).replaceAll('<', '\\u003c');
  return `<!doctype html>
<html lang="es"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="referrer" content="strict-origin-when-cross-origin">
<title>Worker — mapa de diagnóstico</title>
<link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css">
<style>
body{margin:0;font:14px system-ui;color:#17212b}header{padding:12px 18px;background:#f3f5f7}
h1{font-size:20px;margin:0 0 8px}p{margin:6px 0}#status{white-space:pre-line}#map{height:70vh;min-height:400px}
label{display:inline-block;margin:8px 18px 0 0}select{padding:4px}#warning{color:#9c4300} .popup{white-space:pre-line;min-width:220px}
</style>
<header><h1>Worker — mapa de diagnóstico</h1>
<p id="status"></p><p id="warning"></p>
<p>Snapshot estático. Para actualizar, volver a generar el archivo y recargarlo.</p>
<p>Los puntos son la posición media de la última observación de cada aglutinación, no su trayectoria.
Son eventos operacionales sin los filtros de publicación de la API. Los sectores muestran mediciones, no cuellos de botella detectados.</p>
<p>Rojo: un pasaje completo a menos de 6 km/h. Violeta: cuello de botella recurrente experimental, con 2 o más pasajes a menos de 6 km/h en esta ventana.
Incluye detenciones; no exige unidades ni días distintos. 500 m en 5 min = 6 km/h; el corte es estrictamente menor a 6 km/h.
Los extremos del recorrido siguen incluidos; revisar posibles esperas de cabecera.</p>
<label>Línea <select id="line"><option value="">Todas</option></select></label>
<label><input id="events" type="checkbox" checked> Aglutinaciones (azul: abiertas; gris: cerradas)</label>
<label><input id="sectors" type="checkbox" checked> Sectores (violeta: ≥2 pasajes &lt;6 km/h; rojo: 1; verde: 0; naranja: sólo incompletos)</label>
<p id="counts"></p></header><div id="map"></div>
<script id="data" type="application/json">${payload}</script>
<script src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"></script>
<script id="map-code">
const data=JSON.parse(document.getElementById('data').textContent);
const sectorColor=${sectorColor.toString()};
const date=value=>value?new Date(value).toLocaleString('es-AR',{timeZone:'America/Argentina/Buenos_Aires'}):'—';
const status=data.status, capture=status.capture;
const progress=status.processors.map(p=>'Checkpoint '+p.checkpoint+'; capturas pendientes: '+p.pending_captures).join(' | ');
document.getElementById('status').textContent='Generado: '+date(data.generated_at)+' · Ventana: '+data.hours+' h (hora Buenos Aires)\\n'+
  (capture?'Última captura: '+capture.id+' · '+date(capture.received_at)+' · Válidas: '+capture.valid+' · Nuevas: '+capture.inserted+(capture.partial?' · PARCIAL':''):'Sin capturas')+'\\n'+
  (progress||'Productor todavía sin inicializar')+' · Estados: '+status.states+' · Secuencias abiertas: '+status.open_sequences;
const warnings=[];
if(data.events.features.length>data.limit){
  warnings.push('Se muestran sólo los '+data.limit+' eventos más recientes.');
  data.events.features.length=data.limit;
}
if(!data.sectors.features.length)warnings.push('Sin mediciones finales en esta ventana. Revisar checkpoint y secuencias abiertas; no significa ausencia de lentitud.');
document.getElementById('warning').textContent=warnings.join(' ');
if(typeof L==='undefined'){document.getElementById('map').textContent='No se pudo cargar Leaflet. Se requiere Internet para la biblioteca y el mapa base.';}
else{
const map=L.map('map').setView([-34.61,-58.44],11);
if(location.protocol==='file:'){
  document.getElementById('warning').textContent+=' Abrir por HTTP: node scripts/worker-map.mjs --serve y visitar http://127.0.0.1:8088. No se solicitan teselas desde file:// porque no envía Referer.';
}else{
  const tiles=L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png',{
    maxZoom:19,referrerPolicy:'strict-origin-when-cross-origin',
    attribution:'&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
  }).addTo(map);
  let tileErrorShown=false;
  tiles.on('tileerror',()=>{if(!tileErrorShown){tileErrorShown=true;document.getElementById('warning').textContent+=' No cargaron las teselas: revisar red, bloqueadores y Referer. Un bloqueo de OSM puede persistir; no se reintenta ni cambia de servidor automáticamente.';}});
}
const text=value=>{const el=document.createElement('span');el.textContent=value;return el;};
const popup=(p,event)=>{const el=document.createElement('div');el.className='popup';
  el.textContent='Línea '+(p.linea||'sin etiqueta')+' · Ramal '+(p.ramal||'—')+' · Sentido '+p.sentido+'\\n'+
    (event?'Evento '+p.id_evento+' · '+(p.cierre?'Cerrado':'Abierto')+'\\nInicio: '+date(p.inicio)+'\\nÚltima observación: '+date(p.ultima_observacion)+'\\nDuración observada: '+Math.round(p.duracion_segundos/60)+' min · Observaciones: '+p.observaciones+'\\nSeparación última: '+p.distancia_metros+' m'+(p.motivo_cierre?'\\nCierre: '+p.motivo_cierre:''):
    'Sector '+p.sector+' · Trazado '+p.trazado+' · '+p.longitud_metros+' m'+(p.extremo_recorrido?'\\nExtremo del recorrido: revisar regulación/cabecera':'')+'\\nPasajes completos: '+p.pasajes+' · Incompletos: '+p.incompletos+'\\nPasajes <6 km/h: '+p.pasajes_lentos+' / '+p.pasajes+' ('+(p.pasajes>0?(100*p.pasajes_lentos/p.pasajes).toFixed(1)+'%':'sin datos')+')'+(p.pasajes_lentos>=data.recurrent_min_passages?'\\nCuello de botella recurrente (experimental)':'')+'\\nDuración mediana: '+(p.duracion_mediana_min??'sin datos')+' min\\nVelocidad comercial mediana: '+(p.velocidad_mediana_kph??'sin datos')+' km/h\\nÚltima entrada: '+date(p.ultima_entrada));return el;};
const line=document.getElementById('line');
const lines=[...new Set([...data.events.features,...data.sectors.features].map(f=>f.properties.linea||''))].sort((a,b)=>a.localeCompare(b,'es',{numeric:true}));
for(const name of lines){const option=document.createElement('option');option.value=name||'__empty__';option.textContent=name||'Sin etiqueta';line.appendChild(option);}
let layers=[];
function redraw(fit){
  for(const layer of layers)map.removeLayer(layer);layers=[];
  const counts=[];
  for(const key of ['events','sectors']){
    const features=data[key].features.filter(f=>!line.value||(f.properties.linea||'__empty__')===line.value);
    const enabled=document.getElementById(key).checked;
    counts.push((key==='events'?'Eventos':'Sectores')+': '+features.length+(enabled?'':' (ocultos)'));
    if(!enabled)continue;
    const layer=L.geoJSON(features,{
      pointToLayer:(f,ll)=>L.circleMarker(ll,{radius:6,color:f.properties.cierre?'#666':'#1674ce',fillOpacity:0.65}),
      style:f=>key==='events'?{color:f.properties.cierre?'#666':'#1674ce'}:{color:sectorColor(f.properties,data.recurrent_min_passages),weight:f.properties.pasajes_lentos>0?7:5,opacity:0.8},
      onEachFeature:(f,l)=>{l.bindPopup(popup(f.properties,key==='events'));l.bindTooltip(text('Línea '+(f.properties.linea||'—')+' · '+(f.properties.ramal||'—')));}
    }).addTo(map);layers.push(layer);
  }
  document.getElementById('counts').textContent=counts.join(' · ');
  if(fit&&layers.length){const bounds=L.featureGroup(layers).getBounds();if(bounds.isValid())map.fitBounds(bounds,{padding:[20,20],maxZoom:14});}
}
line.addEventListener('change',()=>redraw(true));
for(const key of ['events','sectors'])document.getElementById(key).addEventListener('change',()=>redraw(true));
redraw(true);
}
</script></html>`;
}

// Serve only the chosen snapshot, never a directory or database endpoint.
export function serveMapFile(file, port = 8088) {
  const server = createServer((req, res) => {
    if (!['GET', 'HEAD'].includes(req.method) || !['/', '/worker-map.html'].includes(req.url)) {
      res.writeHead(404); res.end(); return;
    }
    try {
      const html = readFileSync(file);
      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8',
        'Referrer-Policy': 'strict-origin-when-cross-origin', 'X-Content-Type-Options': 'nosniff' });
      res.end(req.method === 'HEAD' ? undefined : html);
    } catch {
      res.writeHead(500); res.end('No se pudo leer el snapshot.');
    }
  });
  server.listen(port, '127.0.0.1');
  return server;
}

function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log('DATABASE_URL=<conexión> node scripts/worker-map.mjs [--hours 6] [--limit 2000] [--output .local/worker-map.html] [--serve] [--port 8088]');
    return;
  }
  if (!process.env.DATABASE_URL) throw new Error('Exportar DATABASE_URL. No se carga .env automáticamente.');
  const query = readFileSync(new URL('./worker-map.sql', import.meta.url), 'utf8');
  const result = spawnSync('psql', ['-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-v', 'hours='+options.hours, '-v', 'row_limit='+options.limit], {
    input: query, encoding: 'utf8', timeout: 25000, maxBuffer: 32 * 1024 * 1024,
    env: databaseEnvironment(process.env.DATABASE_URL),
  });
  if (result.error?.code === 'ENOENT') throw new Error('No se encuentra psql en PATH. Usar el cliente PostgreSQL o Nix.');
  if (result.error || result.status !== 0) throw new Error('No se pudo consultar PostgreSQL: revisar conexión, esquema y timeout. No se modificaron datos.');
  const data = JSON.parse(result.stdout);
  const output = resolve(options.output);
  mkdirSync(dirname(output), { recursive: true });
  writeFileSync(output, renderHTML(data), { mode: 0o600 });
  console.log('Mapa generado: '+output);
  if (options.serve) {
    const server = serveMapFile(output, options.port ?? 8088);
    server.on('listening', () => console.log('Abrir http://127.0.0.1:'+server.address().port+' · Ctrl+C para detener.'));
    server.on('error', () => { console.error('No se pudo abrir el servidor local; revisar el puerto o usar --port.'); process.exitCode = 1; });
  } else {
    console.log('Para ver el mapa base por HTTP: node scripts/worker-map.mjs --serve');
  }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try { main(); } catch (error) {
    console.error(error instanceof SyntaxError ? 'PostgreSQL devolvió un snapshot no válido.' : error.message);
    process.exitCode = 1;
  }
}
