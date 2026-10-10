#!/usr/bin/env node

import fs from 'node:fs';
import readline from 'node:readline';
import zlib from 'node:zlib';

const options = parseArgs(process.argv.slice(2));
const required = ['captureDirectory', 'gtfsDirectory', 'output'];
for (const name of required) {
  if (!options[name]) {
    console.error('uso: node scripts/match-gcba-spatial.mjs --captures DIR --gtfs DIR --output ARCHIVO.json [--sample-size 800]');
    process.exit(2);
  }
}

const sampleSize = Number(options.sampleSize ?? 800);
if (!Number.isInteger(sampleSize) || sampleSize < 50) throw new Error('--sample-size debe ser un entero >= 50');

function parseArgs(args) {
  const result = {};
  const names = { '--captures': 'captureDirectory', '--gtfs': 'gtfsDirectory', '--output': 'output', '--sample-size': 'sampleSize' };
  for (let index = 0; index < args.length; index++) {
    const name = names[args[index]];
    if (!name || index + 1 >= args.length) throw new Error(`argumento inválido: ${args[index]}`);
    result[name] = args[++index];
  }
  return result;
}

function parseCSVLine(line) {
  const fields = [];
  let field = '', quoted = false;
  for (let index = 0; index < line.length; index++) {
    const char = line[index];
    if (char === '"') {
      if (quoted && line[index + 1] === '"') field += line[++index];
      else quoted = !quoted;
    } else if (char === ',' && !quoted) {
      fields.push(field); field = '';
    } else field += char;
  }
  fields.push(field.replace(/\r$/, ''));
  return fields;
}

async function readCSV(path, visit) {
  const input = readline.createInterface({ input: fs.createReadStream(path), crlfDelay: Infinity });
  let header;
  for await (const line of input) {
    if (!header) { header = parseCSVLine(line); continue; }
    if (!line) continue;
    const fields = parseCSVLine(line);
    const row = Object.fromEntries(header.map((name, index) => [name, fields[index] ?? '']));
    visit(row);
  }
}

const normalize = value => String(value ?? '').trim().normalize('NFD').replace(/\p{Diacritic}/gu, '').toUpperCase().replace(/\s+/g, ' ');
const lineOf = shortName => normalize(shortName).match(/^(\d+|[A-Z]+)/)?.[1] ?? normalize(shortName);
const identityKey = (routeID, direction) => `${routeID}\0${direction}`;
const targetKey = (routeID, direction) => `${routeID}\0${direction}`;
const append = (map, key, value) => {
  if (!map.has(key)) map.set(key, []);
  map.get(key).push(value);
};

console.error('leyendo agency.txt y routes.txt...');
const agencies = new Map();
const agencyIDsByName = new Map();
await readCSV(`${options.gtfsDirectory}/agency.txt`, row => {
  agencies.set(row.agency_id, row.agency_name);
  append(agencyIDsByName, normalize(row.agency_name), row.agency_id);
});
const routes = new Map();
await readCSV(`${options.gtfsDirectory}/routes.txt`, row => {
  if (row.agency_id === '446') return;
  routes.set(row.route_id, {
    routeID: row.route_id,
    agencyID: row.agency_id,
    agencyName: agencies.get(row.agency_id) ?? '',
    shortName: row.route_short_name,
    line: lineOf(row.route_short_name),
    description: row.route_desc,
  });
});

console.error('leyendo trips.txt...');
const targets = [];
const targetByKey = new Map();
const neededShapes = new Set();
await readCSV(`${options.gtfsDirectory}/trips.txt`, row => {
  if (row.exceptional !== '0' || !routes.has(row.route_id)) return;
  const key = targetKey(row.route_id, row.direction_id);
  const existing = targetByKey.get(key);
  if (existing) {
    if (existing.shapeID !== row.shape_id) throw new Error(`más de un shape regular para ${row.route_id}/${row.direction_id}`);
    return;
  }
  const route = routes.get(row.route_id);
  const target = {
    key,
    routeID: row.route_id,
    direction: Number(row.direction_id),
    shapeID: row.shape_id,
    shortName: route.shortName,
    line: route.line,
    agencyID: route.agencyID,
    agencyName: route.agencyName,
    description: route.description,
    headsign: row.trip_headsign,
  };
  targetByKey.set(key, target);
  targets.push(target);
  neededShapes.add(row.shape_id);
});

console.error(`leyendo shapes.txt para ${neededShapes.size} shapes...`);
const rawShapes = new Map();
await readCSV(`${options.gtfsDirectory}/shapes.txt`, row => {
  if (!neededShapes.has(row.shape_id)) return;
  let points = rawShapes.get(row.shape_id);
  if (!points) { points = []; rawShapes.set(row.shape_id, points); }
  points.push([Number(row.shape_pt_sequence), Number(row.shape_pt_lat), Number(row.shape_pt_lon)]);
});

const metersPerLatitude = 111_320;
const metersPerLongitude = 91_300;
const cellMeters = 250;
const cellKey = (x, y) => (x + 50_000) * 100_000 + y + 50_000;

function buildShape(rawPoints) {
  rawPoints.sort((a, b) => a[0] - b[0]);
  const xs = new Float64Array(rawPoints.length);
  const ys = new Float64Array(rawPoints.length);
  for (let index = 0; index < rawPoints.length; index++) {
    ys[index] = rawPoints[index][1] * metersPerLatitude;
    xs[index] = rawPoints[index][2] * metersPerLongitude;
  }
  const grid = new Map();
  for (let index = 0; index + 1 < xs.length; index++) {
    const minX = Math.floor(Math.min(xs[index], xs[index + 1]) / cellMeters);
    const maxX = Math.floor(Math.max(xs[index], xs[index + 1]) / cellMeters);
    const minY = Math.floor(Math.min(ys[index], ys[index + 1]) / cellMeters);
    const maxY = Math.floor(Math.max(ys[index], ys[index + 1]) / cellMeters);
    for (let x = minX; x <= maxX; x++) for (let y = minY; y <= maxY; y++) {
      const key = cellKey(x, y);
      if (!grid.has(key)) grid.set(key, []);
      grid.get(key).push(index);
    }
  }
  return { xs, ys, grid };
}

console.error('indexando geometrías...');
const shapes = new Map();
const shapesByCell = new Map();
for (const shapeID of neededShapes) {
  const points = rawShapes.get(shapeID);
  if (!points || points.length < 2) throw new Error(`shape ${shapeID} sin geometría suficiente`);
  const shape = buildShape(points);
  shapes.set(shapeID, shape);
  for (const key of shape.grid.keys()) {
    if (!shapesByCell.has(key)) shapesByCell.set(key, []);
    shapesByCell.get(key).push(shapeID);
  }
}
rawShapes.clear();

function hash32(text) {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index++) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

console.error('leyendo capturas realtime...');
const metadata = fs.readFileSync(`${options.captureDirectory}/metadata.ndjson`, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse).sort((a, b) => new Date(a.finishedAt) - new Date(b.finishedAt));
const fileByHash = new Map(metadata.filter(row => row.file).map(row => [row.sha256, row.file]));
const identities = new Map();
let previousHash = '';
let revisions = 0;

for (const capture of metadata) {
  if (capture.status !== 200 || !capture.sha256 || capture.sha256 === previousHash || capture.vehicles < 5000) continue;
  previousHash = capture.sha256;
  const file = fileByHash.get(capture.sha256);
  if (!file) throw new Error(`falta el cuerpo ${capture.sha256}`);
  const positions = JSON.parse(zlib.gunzipSync(fs.readFileSync(`${options.captureDirectory}/${file}`)));
  const capturedAt = new Date(capture.finishedAt).getTime() / 1000;
  revisions++;
  for (const position of positions) {
    if (!Number.isFinite(position.latitude) || !Number.isFinite(position.longitude) || !Number.isFinite(position.timestamp)) continue;
    const age = capturedAt - position.timestamp;
    if (age < -60 || age > 300) continue;
    const key = identityKey(position.route_id, position.direction);
    let identity = identities.get(key);
    if (!identity) {
      identity = {
        key,
        routeID: String(position.route_id),
        direction: Number(position.direction),
        shortNames: new Set(),
        agencyIDs: new Set(),
        agencyNames: new Set(),
        headsigns: new Set(),
        samples: [],
        seen: 0,
        lastTimestampByVehicle: new Map(),
      };
      identities.set(key, identity);
    }
    const vehicle = String(position.id);
    const previousTimestamp = identity.lastTimestampByVehicle.get(vehicle);
    if (previousTimestamp !== undefined && position.timestamp <= previousTimestamp) continue;
    identity.lastTimestampByVehicle.set(vehicle, position.timestamp);
    identity.shortNames.add(String(position.route_short_name));
    identity.agencyIDs.add(String(position.agency_id));
    identity.agencyNames.add(String(position.agency_name));
    identity.headsigns.add(String(position.trip_headsign));
    identity.seen++;
    const point = { lat: position.latitude, lon: position.longitude, timestamp: position.timestamp, vehicle };
    if (identity.samples.length < sampleSize) identity.samples.push(point);
    else {
      const slot = hash32(`${vehicle}\0${position.timestamp}`) % identity.seen;
      if (slot < sampleSize) identity.samples[slot] = point;
    }
  }
}

const targetsByAgencyLine = new Map();
const targetsByLine = new Map();
const targetsByAgency = new Map();
const targetsByShape = new Map();
for (const target of targets) {
  append(targetsByAgencyLine, `${target.agencyID}\0${target.line}`, target);
  append(targetsByLine, target.line, target);
  append(targetsByAgency, target.agencyID, target);
  append(targetsByShape, target.shapeID, target);
}

function spatialShapeCandidates(identity) {
  const hits = new Map();
  for (const point of identity.samples) {
    const x = Math.floor(point.lon * metersPerLongitude / cellMeters);
    const y = Math.floor(point.lat * metersPerLatitude / cellMeters);
    const nearby = new Set();
    for (let dx = -1; dx <= 1; dx++) for (let dy = -1; dy <= 1; dy++) {
      for (const shapeID of shapesByCell.get(cellKey(x + dx, y + dy)) ?? []) nearby.add(shapeID);
    }
    for (const shapeID of nearby) hits.set(shapeID, (hits.get(shapeID) ?? 0) + 1);
  }
  return [...hits].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0], 'es', { numeric: true })).slice(0, 15);
}

function candidatesFor(identity) {
  const result = new Map();
  const add = candidates => { for (const candidate of candidates ?? []) result.set(candidate.key, candidate); };
  add([targetByKey.get(targetKey(identity.routeID, identity.direction)), targetByKey.get(targetKey(identity.routeID, 1 - identity.direction))].filter(Boolean));
  const lines = new Set([...identity.shortNames].map(lineOf));
  const compatibleAgencyIDs = new Set(identity.agencyIDs);
  for (const agencyName of identity.agencyNames) for (const agencyID of agencyIDsByName.get(normalize(agencyName)) ?? []) compatibleAgencyIDs.add(agencyID);
  for (const agency of compatibleAgencyIDs) {
    for (const line of lines) add(targetsByAgencyLine.get(`${agency}\0${line}`));
    add(targetsByAgency.get(agency));
  }
  for (const line of lines) add(targetsByLine.get(line));
  for (const [shapeID] of spatialShapeCandidates(identity)) add(targetsByShape.get(shapeID));
  return [...result.values()];
}

function nearest(shape, point) {
  const px = point.lon * metersPerLongitude;
  const py = point.lat * metersPerLatitude;
  const cellX = Math.floor(px / cellMeters), cellY = Math.floor(py / cellMeters);
  let bestSquared = 1_500 * 1_500, bestAlong = null;
  const visited = new Set();
  for (let dx = -2; dx <= 2; dx++) for (let dy = -2; dy <= 2; dy++) {
    for (const index of shape.grid.get(cellKey(cellX + dx, cellY + dy)) ?? []) {
      if (visited.has(index)) continue;
      visited.add(index);
      const ax = shape.xs[index], ay = shape.ys[index];
      const bx = shape.xs[index + 1], by = shape.ys[index + 1];
      const vx = bx - ax, vy = by - ay;
      const lengthSquared = vx * vx + vy * vy;
      const t = lengthSquared === 0 ? 0 : Math.max(0, Math.min(1, ((px - ax) * vx + (py - ay) * vy) / lengthSquared));
      const qx = ax + t * vx, qy = ay + t * vy;
      const squared = (px - qx) ** 2 + (py - qy) ** 2;
      if (squared < bestSquared) {
        bestSquared = squared;
        bestAlong = (index + t) / (shape.xs.length - 1);
      }
    }
  }
  return { distance: Math.sqrt(bestSquared), along: bestAlong };
}

function percentile(sorted, fraction) {
  if (!sorted.length) return null;
  return sorted[Math.min(sorted.length - 1, Math.floor((sorted.length - 1) * fraction))];
}

function scoreCandidate(identity, candidate) {
  const shape = shapes.get(candidate.shapeID);
  const identityLines = new Set([...identity.shortNames].map(lineOf));
  const normalizedShortNames = new Set([...identity.shortNames].map(normalize));
  const projections = identity.samples.map(point => ({ ...nearest(shape, point), point }));
  const distances = projections.map(value => value.distance).sort((a, b) => a - b);
  const within100 = distances.filter(distance => distance <= 100).length / distances.length;
  const within200 = distances.filter(distance => distance <= 200).length / distances.length;
  const coveredShapeBins = new Set(projections
    .filter(value => value.distance <= 200 && value.along !== null)
    .map(value => Math.min(19, Math.floor(value.along * 20))));
  const shapeCoverage = coveredShapeBins.size / 20;

  const byVehicle = new Map();
  for (const projection of projections) {
    if (projection.distance > 200 || projection.along === null) continue;
    append(byVehicle, projection.point.vehicle, projection);
  }
  let forward = 0, backward = 0;
  for (const values of byVehicle.values()) {
    values.sort((a, b) => a.point.timestamp - b.point.timestamp);
    for (let index = 1; index < values.length; index++) {
      const previous = values[index - 1], current = values[index];
      const elapsed = current.point.timestamp - previous.point.timestamp;
      const delta = current.along - previous.along;
      if (elapsed <= 0 || elapsed > 600 || Math.abs(delta) < 0.001 || Math.abs(delta) > 0.25) continue;
      if (delta > 0) forward++; else backward++;
    }
  }
  const directionPairs = forward + backward;
  const directionAgreement = directionPairs ? forward / directionPairs : null;
  const directionComponent = directionAgreement ?? 0.5;
  const score = 0.35 * within200 + 0.25 * within100 + 0.20 * shapeCoverage + 0.20 * directionComponent;
  return {
    routeID: candidate.routeID,
    direction: candidate.direction,
    shortName: candidate.shortName,
    agencyID: candidate.agencyID,
    agencyName: candidate.agencyName,
    headsign: candidate.headsign,
    shapeID: candidate.shapeID,
    line: candidate.line,
    lineCompatible: identityLines.has(candidate.line),
    agencyCompatible: identity.agencyIDs.has(candidate.agencyID) || [...identity.agencyNames].some(name => normalize(name) === normalize(candidate.agencyName)),
    shortNameExact: normalizedShortNames.has(normalize(candidate.shortName)),
    score: Number(score.toFixed(6)),
    coverage100m: Number(within100.toFixed(6)),
    coverage200m: Number(within200.toFixed(6)),
    shapeCoverage20Bins: Number(shapeCoverage.toFixed(6)),
    medianDistanceM: Number(percentile(distances, 0.5).toFixed(1)),
    p95DistanceM: Number(percentile(distances, 0.95).toFixed(1)),
    directionAgreement: directionAgreement === null ? null : Number(directionAgreement.toFixed(6)),
    directionPairs,
  };
}

console.error(`puntuando ${identities.size} identidades contra ${targets.length} recorridos...`);
const matches = [];
let processed = 0;
for (const identity of identities.values()) {
  const candidates = candidatesFor(identity);
  const scored = candidates.map(candidate => scoreCandidate(identity, candidate));
  const compareScores = (a, b) => b.score - a.score || b.coverage100m - a.coverage100m || a.routeID.localeCompare(b.routeID, 'es', { numeric: true }) || a.direction - b.direction;
  const spatialRanking = [...scored].sort(compareScores);
  const exactShortNameRanking = scored.filter(candidate => candidate.shortNameExact).sort(compareScores);
  const agencyRanking = scored.filter(candidate => candidate.agencyCompatible).sort(compareScores);
  const lineRanking = scored.filter(candidate => candidate.lineCompatible).sort(compareScores);
  const primaryRanking = agencyRanking[0]?.coverage200m >= 0.25 ? agencyRanking : lineRanking;
  matches.push({
    gcbaRouteID: identity.routeID,
    gcbaDirection: identity.direction,
    routeShortNames: [...identity.shortNames].sort(),
    agencyIDs: [...identity.agencyIDs].sort(),
    agencyNames: [...identity.agencyNames].sort(),
    headsigns: [...identity.headsigns].sort(),
    observations: identity.seen,
    samples: identity.samples.length,
    candidateCount: candidates.length,
    exactGTFSIdentityExists: targetByKey.has(targetKey(identity.routeID, identity.direction)),
    rankingBasis: primaryRanking === agencyRanking ? 'agency' : 'line',
    ranking: primaryRanking.slice(0, 3),
    exactShortNameRanking: exactShortNameRanking.slice(0, 3),
    spatialRanking: spatialRanking.slice(0, 3),
  });
  if (++processed % 100 === 0) console.error(`  ${processed}/${identities.size}`);
}

matches.sort((a, b) => a.routeShortNames.join().localeCompare(b.routeShortNames.join(), 'es', { numeric: true }) || a.gcbaDirection - b.gcbaDirection || a.gcbaRouteID.localeCompare(b.gcbaRouteID, 'es', { numeric: true }));
const output = {
  generatedAt: new Date().toISOString(),
  inputs: { captures: options.captureDirectory, gtfs: options.gtfsDirectory, revisions, sampleSize },
  counts: { identities: identities.size, gtfsTargets: targets.length },
  matches,
};
fs.writeFileSync(options.output, `${JSON.stringify(output, null, 2)}\n`);
console.log(JSON.stringify({ ...output.counts, revisions, output: options.output }, null, 2));
