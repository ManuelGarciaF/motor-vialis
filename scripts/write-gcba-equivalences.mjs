#!/usr/bin/env node

import fs from 'node:fs';

const [inputCSV, outputSQL] = process.argv.slice(2);
if (!inputCSV || !outputSQL) {
  console.error('uso: node scripts/write-gcba-equivalences.mjs CANDIDATOS.csv SALIDA.sql');
  process.exit(2);
}

function parseCSV(text) {
  const rows = [];
  let row = [], field = '', quoted = false;
  for (let index = 0; index < text.length; index++) {
    const char = text[index];
    if (char === '"') {
      if (quoted && text[index + 1] === '"') field += text[++index];
      else quoted = !quoted;
    } else if (char === ',' && !quoted) {
      row.push(field); field = '';
    } else if ((char === '\n' || char === '\r') && !quoted) {
      if (char === '\r' && text[index + 1] === '\n') index++;
      row.push(field); field = '';
      if (row.some(value => value !== '')) rows.push(row);
      row = [];
    } else field += char;
  }
  if (field || row.length) { row.push(field); rows.push(row); }
  const [header, ...values] = rows;
  return values.map(columns => Object.fromEntries(header.map((name, index) => [name, columns[index] ?? ''])));
}

const quote = value => `'${String(value).replaceAll("'", "''")}'`;
const acceptedClassifications = new Set(['high_confidence', 'ambiguous']);
const matches = parseCSV(fs.readFileSync(inputCSV, 'utf8'))
  .filter(row => acceptedClassifications.has(row.classification))
  .sort((a, b) => a.gcba_route_id.localeCompare(b.gcba_route_id, 'es', { numeric: true }) || Number(a.gcba_direction) - Number(b.gcba_direction));

const keys = new Set();
for (const match of matches) {
  const key = `${match.gcba_route_id}\0${match.gcba_direction}`;
  if (keys.has(key)) throw new Error(`identidad GCBA duplicada: ${match.gcba_route_id}/${match.gcba_direction}`);
  keys.add(key);
  if (![match.gcba_direction, match.gtfs_direction_id].every(value => value === '0' || value === '1')) {
    throw new Error(`dirección inválida en ${match.gcba_route_id}/${match.gcba_direction}`);
  }
  if (!match.gtfs_route_id) throw new Error(`destino GTFS vacío en ${match.gcba_route_id}/${match.gcba_direction}`);
}

const values = matches.map(match => `    (${quote(match.gcba_route_id)}, ${match.gcba_direction}, ${quote(match.gtfs_route_id)}, ${match.gtfs_direction_id})`).join(',\n');
const sql = `\\set ON_ERROR_STOP on

-- Fuente versionada de las equivalencias definitivas entre la API realtime de
-- GCBA y GTFS. Se genera desde docs/h0011_spatial_match_candidates.csv con
-- scripts/write-gcba-equivalences.mjs. Incluye classification=high_confidence
-- y, para classification=ambiguous, el candidato con mayor score.

BEGIN;

TRUNCATE TABLE vialis.equivalencias_recorridos_gcba;

INSERT INTO vialis.equivalencias_recorridos_gcba (
    gcba_route_id,
    gcba_direction,
    gtfs_route_id,
    gtfs_direction_id
) VALUES
${values};

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM vialis.equivalencias_recorridos_gcba equivalencia
        LEFT JOIN vialis.recorridos recorrido
          ON recorrido.gtfs_route_id = equivalencia.gtfs_route_id
         AND recorrido.direction_id = equivalencia.gtfs_direction_id
        WHERE recorrido.id_recorrido IS NULL
    ) THEN
        RAISE EXCEPTION 'Hay equivalencias GCBA con destinos ausentes del GTFS cargado';
    END IF;
END
$$;

COMMIT;
`;

fs.writeFileSync(outputSQL, sql);
const classifications = Object.fromEntries([...acceptedClassifications].map(classification => [
  classification,
  matches.filter(match => match.classification === classification).length,
]));
console.log(JSON.stringify({ matches: matches.length, classifications, output: outputSQL }, null, 2));
