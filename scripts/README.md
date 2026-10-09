# Herramientas administrativas

No forman parte de la API ni del worker. Se conservan sólo las herramientas de
auditoría y mantenimiento de las equivalencias GCBA–GTFS.

## Auditoría de aglutinaciones

Snapshot consistente, sin escrituras, compatible con el worker en ejecución:

```sh
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -f scripts/audit-h0011-bunching.sql
```

## Captura para revisar equivalencias

`capture-gcba.mjs` guarda respuestas completas para el matching, incluidas las
identidades que el worker ignora por no tener equivalencia. Requiere Node.js 20+
y credenciales GCBA desde `.env` o el entorno; no registra URLs autenticadas.
No sustituye al worker ni debe mantenerse como un segundo poller permanente.

```sh
node scripts/capture-gcba.mjs --hours 10
# Prueba offline, sin credenciales ni requests:
node scripts/capture-gcba.mjs --self-test
```

Salida en `.local/gcba-captures/<inicio>/`: cuerpos distintos `*.json.gz`,
`metadata.ndjson` y `summary.json`. Requests secuenciales, timeout 120 s y espera
30 s después de cada request, con backoff y `Retry-After`. `--output` permite
reanudar el directorio. SIGINT/SIGTERM detienen la captura. Las posiciones e IDs
reales permanecen en `.local`, nunca en Git.

## Regenerar el mapping espacial

Usar el snapshot GTFS que corresponda al catálogo que se pretende cargar.
Comparar muestras deterministas de 500 y 800 posiciones y revisar los candidatos
antes de reemplazar equivalencias:

```sh
mkdir -p .local/h0011
for size in 500 800; do
  node scripts/match-gcba-spatial.mjs \
    --captures .local/gcba-captures/<inicio> \
    --gtfs .local/h0011/reload-gtfs \
    --sample-size "$size" \
    --output ".local/h0011/spatial-matches-$size.json"
done
node scripts/select-gcba-spatial-matches.mjs \
  .local/h0011/spatial-matches-800.json \
  .local/h0011/spatial-matches-500.json \
  docs/h0011_spatial_match_candidates.csv
node scripts/write-gcba-equivalences.mjs \
  docs/h0011_spatial_match_candidates.csv \
  sql/recorridos/cargar_equivalencias_gcba.sql
```

La política administrada actual acepta `high_confidence` y el mejor candidato de
`ambiguous`; no significa que todos tengan confianza alta. El CSV conserva las
clasificaciones y no contiene IDs de vehículos. El SQL generado reemplaza la
tabla de equivalencias: no ejecutarlo automáticamente durante una request.

Criterios y decisiones: [`../docs/plan_h0011_aglutinacion.md`](../docs/plan_h0011_aglutinacion.md).
Operación del worker/API: [`../docs/h0011_aglutinaciones.md`](../docs/h0011_aglutinaciones.md).
