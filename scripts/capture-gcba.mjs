#!/usr/bin/env node

import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import fsp from 'node:fs/promises';
import path from 'node:path';
import zlib from 'node:zlib';

const endpoint = 'https://apitransporte.buenosaires.gob.ar/colectivos/vehiclePositionsSimple';
const maximumBodyBytes = 10 * 1024 * 1024;
const requestTimeoutMs = 120_000;

function parseArgs(args) {
  const options = { hours: 0, intervalSeconds: 30, output: '' };
  for (let i = 0; i < args.length; i++) {
    const [name, inlineValue] = args[i].split('=', 2);
    if (name === '--help' || name === '--self-test') {
      options[name.slice(2).replace('-', '')] = true;
      continue;
    }
    const value = inlineValue ?? args[++i];
    if (value === undefined) throw new Error(`falta el valor de ${name}`);
    if (name === '--hours') options.hours = positiveNumber(value, name, true);
    else if (name === '--interval-seconds') options.intervalSeconds = positiveNumber(value, name);
    else if (name === '--output') options.output = value;
    else throw new Error(`opción desconocida: ${name}`);
  }
  return options;
}

function positiveNumber(value, name, allowZero = false) {
  const number = Number(value);
  if (!Number.isFinite(number) || (allowZero ? number < 0 : number <= 0)) {
    throw new Error(`${name} debe ser un número ${allowZero ? 'no negativo' : 'positivo'}`);
  }
  return number;
}

function safeHeaders(headers) {
  return Object.fromEntries([...headers].filter(([name]) =>
    ['content-length', 'content-type', 'date', 'retry-after'].includes(name) || /rate.?limit/i.test(name),
  ));
}

function usage() {
  console.log(`Uso: node scripts/capture-gcba.mjs [opciones]

Opciones:
  --hours N             duración; 0 ejecuta hasta recibir SIGINT/SIGTERM (default: 0)
  --interval-seconds N  espera después de cada request (default: 30)
  --output DIR          directorio de salida (default: .local/gcba-captures/<inicio>)
  --self-test           ejecuta verificaciones locales y termina
  --help                muestra esta ayuda

Lee GCBA_TRANSPORT_CLIENT_ID y GCBA_TRANSPORT_CLIENT_SECRET del entorno o .env.
Guarda sólo respuestas distintas como JSON gzip y registra todos los intentos en metadata.ndjson.`);
}

function selfTest() {
  assert.deepEqual(parseArgs(['--hours', '10', '--interval-seconds=45', '--output', 'x']), {
    hours: 10,
    intervalSeconds: 45,
    output: 'x',
  });
  assert.throws(() => parseArgs(['--interval-seconds', '0']));
  const body = Buffer.from('[{"id":"vehicle-1"}]');
  assert.deepEqual(JSON.parse(zlib.gunzipSync(zlib.gzipSync(body))), [{ id: 'vehicle-1' }]);
  console.log('self-test OK');
}

const options = parseArgs(process.argv.slice(2));
if (options.help) {
  usage();
  process.exit(0);
}
if (options.selftest) {
  selfTest();
  process.exit(0);
}

if (fs.existsSync('.env') && process.loadEnvFile) process.loadEnvFile('.env');
const clientID = process.env.GCBA_TRANSPORT_CLIENT_ID;
const clientSecret = process.env.GCBA_TRANSPORT_CLIENT_SECRET;
if (!clientID || !clientSecret) {
  throw new Error('faltan GCBA_TRANSPORT_CLIENT_ID o GCBA_TRANSPORT_CLIENT_SECRET');
}

const runStartedAt = new Date();
const output = options.output || path.join('.local', 'gcba-captures', runStartedAt.toISOString().replaceAll(':', '-'));
const metadataPath = path.join(output, 'metadata.ndjson');
await fsp.mkdir(output, { recursive: true });

const knownHashes = new Set();
if (fs.existsSync(metadataPath)) {
  for (const line of fs.readFileSync(metadataPath, 'utf8').split('\n')) {
    if (!line) continue;
    try {
      const metadata = JSON.parse(line);
      if (metadata.sha256) knownHashes.add(metadata.sha256);
    } catch {
      throw new Error(`${metadataPath} contiene una línea JSON inválida`);
    }
  }
}

const deadline = options.hours ? runStartedAt.getTime() + options.hours * 60 * 60 * 1000 : Infinity;
let stopping = false;
let activeController;
let attempts = 0;
let successes = 0;
let saved = 0;
let failures = 0;

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    stopping = true;
    activeController?.abort();
  });
}

while (!stopping && Date.now() < deadline) {
  const startedAt = new Date();
  attempts++;
  const url = new URL(endpoint);
  url.searchParams.set('client_id', clientID);
  url.searchParams.set('client_secret', clientSecret);
  activeController = new AbortController();
  let timedOut = false;
  const timeout = setTimeout(() => {
    timedOut = true;
    activeController.abort();
  }, requestTimeoutMs);
  let metadata;
  let retryAfterSeconds = 0;

  try {
    const response = await fetch(url, { signal: activeController.signal });
    const declaredLength = Number(response.headers.get('content-length') || 0);
    if (declaredLength > maximumBodyBytes) throw new Error(`body declarado de ${declaredLength} bytes excede el límite`);
    const body = Buffer.from(await response.arrayBuffer());
    if (body.length > maximumBodyBytes) throw new Error(`body de ${body.length} bytes excede el límite`);
    if (!response.ok) {
      const error = new Error(`HTTP ${response.status}`);
      error.status = response.status;
      error.headers = safeHeaders(response.headers);
      throw error;
    }

    const parsed = JSON.parse(body);
    if (!Array.isArray(parsed)) throw new Error('la respuesta JSON no es un array');

    const sha256 = crypto.createHash('sha256').update(body).digest('hex');
    let file = null;
    if (!knownHashes.has(sha256)) {
      file = `${startedAt.toISOString().replaceAll(':', '-')}-${sha256.slice(0, 12)}.json.gz`;
      const target = path.join(output, file);
      await fsp.writeFile(`${target}.tmp`, zlib.gzipSync(body));
      await fsp.rename(`${target}.tmp`, target);
      knownHashes.add(sha256);
      saved++;
    }
    successes++;
    failures = 0;
    metadata = {
      startedAt: startedAt.toISOString(),
      finishedAt: new Date().toISOString(),
      status: response.status,
      bytes: body.length,
      vehicles: parsed.length,
      sha256,
      file,
      duplicate: file === null,
      headers: safeHeaders(response.headers),
    };
  } catch (error) {
    failures++;
    metadata = {
      startedAt: startedAt.toISOString(),
      finishedAt: new Date().toISOString(),
      status: error.status,
      headers: error.headers,
      error: timedOut ? `request timeout after ${requestTimeoutMs / 1000}s` : `${error.name}: ${error.message}`,
    };
    if (error.status === 429) retryAfterSeconds = 60;
  } finally {
    clearTimeout(timeout);
    activeController = undefined;
  }

  await fsp.appendFile(metadataPath, `${JSON.stringify(metadata)}\n`);
  console.log(JSON.stringify({ attempt: attempts, status: metadata.status, vehicles: metadata.vehicles, bytes: metadata.bytes, saved: metadata.file !== null && metadata.file !== undefined, error: metadata.error }));

  if (stopping || Date.now() >= deadline) break;
  const backoffSeconds = failures ? Math.min(300, options.intervalSeconds * 2 ** Math.min(failures, 4)) : options.intervalSeconds;
  const retryAfter = Number(metadata.headers?.['retry-after']);
  if (Number.isFinite(retryAfter) && retryAfter > 0) retryAfterSeconds = retryAfter;
  await new Promise(resolve => setTimeout(resolve, Math.max(backoffSeconds, retryAfterSeconds) * 1000));
}

const summary = {
  startedAt: runStartedAt.toISOString(),
  finishedAt: new Date().toISOString(),
  attempts,
  successes,
  failures: attempts - successes,
  distinctCapturesSaved: saved,
};
await fsp.writeFile(path.join(output, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`);
console.log(JSON.stringify(summary));
