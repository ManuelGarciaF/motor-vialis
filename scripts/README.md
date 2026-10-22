# Herramientas administrativas

No forman parte de la API ni del worker. Incluyen auditoría, backfills one-off,
mantenimiento de equivalencias GCBA–GTFS y spikes exploratorios.

## Mapa de diagnóstico del worker

Con Node >=20, `psql` en PATH y `DATABASE_URL` exportada (URI PostgreSQL):

```sh
node scripts/worker-map.mjs --serve
# Abrir http://127.0.0.1:8088 (Ctrl+C detiene el servidor).
# Opcional: ventana y tamaño máximo de eventos (sectores sin límite)
node scripts/worker-map.mjs --hours 24 --limit 2000 --output .local/worker-map.html
# Cliente temporal si falta psql
nix shell nixpkgs#postgresql -c node scripts/worker-map.mjs --serve
# Tests sin DB ni red
node --test scripts/test-worker-map.mjs
```

Abrir `http://127.0.0.1:8088`, no el archivo con `file://`: OSM exige un
`Referer` válido desde páginas web. El servidor sólo escucha en loopback y sólo
expone el snapshot, sin listar directorios ni consultar DB por HTTP. Usar
`--port 8089` si el puerto está ocupado.

Es un snapshot estático: ejecutar de nuevo el generador sin `--serve` y recargar
la página para actualizar, o detener/reiniciar el comando con `--serve`. Lee en una
transacción read-only con timeout; no inicia otro worker ni captura GPS. No carga
`.env` automáticamente. Leaflet y las teselas OpenStreetMap requieren Internet.
La [política de teselas OSM](https://operations.osmfoundation.org/policies/tiles/)
requiere atribución visible, URL canónica, Referer y caché normal del navegador.
No hay proxy, descarga anticipada, suplantación de headers ni cambio automático
de proveedor ante bloqueos. Abierto con `file://`, no solicita teselas.

Permite filtrar por línea y alternar dos capas:
- Aglutinaciones operacionales: abiertas/cerradas, línea, ramal, sentido,
  observaciones y duración. El punto es el centro de las últimas posiciones del
  par, no su recorrido. No aplica los filtros de publicación de la API.
- Sectores con pasajes: completos/incompletos, longitud real, duración y velocidad
  comercial medianas. **Violeta**: cuello de botella recurrente experimental,
  con **2 o más pasajes completos a menos de 6 km/h** dentro de la ventana.
  Rojo: un solo pasaje lento; verde: completos sin pasajes lentos;
  naranja: sólo incompletos. El popup muestra cantidad y porcentaje de
  pasajes <6 km/h, sin usar incompletos como velocidades ni como denominador.
  Para 500 m, 5 min equivalen a 6 km/h; se usa velocidad real y el corte es
  estrictamente <6 km/h, incluso en sectores de longitud diferente.
  Incluye detenciones por pasajeros y puede contar varios pasajes de una unidad.
  Los sectores primero/último siguen incluidos y se identifican en el popup
  para revisar regulación de cabecera. No exige unidades ni fechas distintas;
  la recurrencia usa sólo el conteo inclusivo ≥2 en la ventana seleccionada.
  Son **alertas exploratorias, no detecciones definitivas**.

Incluye última captura, checkpoint, capturas pendientes y secuencias OPEN.
Los sectores se exportan sin límite de cantidad dentro de la ventana elegida.
`--limit` sólo limita los eventos de aglutinación y avisa si los trunca.
No exporta IDs
vehiculares ni series GPS raw. El HTML contiene ubicaciones operacionales: dejarlo
en `.local`, no publicarlo ni incorporarlo a Git. No sobrescribir otros archivos
con `--output`.

## Spike H0012: segmentación GPS

Consulta sólo lectura, timeout de cinco minutos y snapshot consistente por
corrida. Requiere PostGIS y raw retenido; no detiene ni modifica el worker.
Defaults: últimas 24 horas terminadas en el último GPS retenido, hueco máximo
180 s, distancia al shape 100 m y velocidad longitudinal máxima 100 km/h.
No usa los filtros de publicación de aglutinaciones.

```sh
mkdir -p .local/h0012
psql "$DATABASE_URL" -X -qAt -f scripts/spike-bottlenecks-segments.sql \
  > .local/h0012/segments-default.json
# Fijar siempre la misma ventana para comparar experimentos:
psql "$DATABASE_URL" -X -qAt \
  -v from='2026-10-09T23:50:52.000001Z' -v to='2026-10-10T23:50:52.000001Z' \
  -v max_gap_seconds=120 -v max_distance_meters=100 -v max_speed_kph=100 \
  -f scripts/spike-bottlenecks-segments.sql > .local/h0012/segments-gap120.json
# Tests sintéticos: sólo tablas TEMP en la sesión, nada persistente.
python3 scripts/test-bottlenecks-segments.py
# Si faltan herramientas:
nix shell nixpkgs#postgresql nixpkgs#python3 -c python3 scripts/test-bottlenecks-segments.py
```

Cargar `DATABASE_URL` desde el entorno sin imprimir credenciales. Desde el
contenedor de desarrollo el host de la DB puede ser `vialis-motor-db:5432`,
distinto del puerto publicado en el host. Si no hay raw, usar una ventana
explícita: la selección automática del último GPS no tiene un corte disponible.

El matcher usa proyección al punto más cercano y no resuelve ambigüedades en
lazos. No publicar detecciones a partir de este experimento. Plan y resultados:
[`plan_h0012_cuellos_botella.md`](../docs/plan_h0012_cuellos_botella.md),
[`spike_h0012_resultados.md`](../docs/spike_h0012_resultados.md).

## Análisis H0012 en Go sobre PostGIS

`cmd/bottlenecks-analysis` es la siguiente etapa del spike: matcher conservador,
distancias geodésicas y un recorrido/ventana por ejecución. Sólo lectura;
requiere PostGIS >=3.4. No detecta recurrencia ni publica un ranking.

```sh
go run ./cmd/bottlenecks-analysis \
  -line-id 1015 -from '2026-10-09T23:50:52.000001Z' \
  -to '2026-10-10T23:50:52.000001Z' -segment-meters 500 \
  > .local/h0012/analysis-1015-500.json
```

Auditoría espacial complementaria, también sólo lectura y agregada:

```sh
psql "$DATABASE_URL" -X -qAt -v line_id=1015 \
  -v from='2026-10-09T23:50:52.000001Z' -v to='2026-10-10T23:50:52.000001Z' \
  -f scripts/audit-bottlenecks-matching.sql > .local/h0012/audit-1015.json
```

Comparación de equivalencias contra el catálogo completo, sin aplicar cambios:

```sh
psql "$DATABASE_URL" -X -qAt -v line_id=1015 \
  -v from='2026-10-09T23:50:52.000001Z' -v to='2026-10-10T23:50:52.000001Z' \
  -f scripts/audit-bottlenecks-equivalences.sql > .local/h0012/equivalences-1015.json
```

Hallazgos: [`h0012_auditoria_matching.md`](../docs/h0012_auditoria_matching.md).
Los candidatos son sólo espaciales: no prueban identidad ni sentido. Ninguna
herramienta corrige equivalencias ni relaja filtros.

Auditoría global usando explícitamente la tabla de equivalencias activa de H0011:

```sh
psql "$DATABASE_URL" -X -qAt -v compare_catalog=true \
  -v from='2026-10-08T17:01:10Z' -v to='2026-10-11T00:46:52.000001Z' \
  -f scripts/audit-bottlenecks-global.sql > .local/h0012/audit-global-retained-full.json
python3 scripts/test-bottlenecks-global.py
```

Usa tablas/índice TEMP de sesión autorizados para la búsqueda global. Lee fuentes
en snapshot read-only/repeatable-read y no modifica tablas persistentes. Los
fragmentos conservan vértices y aristas originales. Con `compare_catalog=false`
(default) sólo mide contra el asignado; los conteos de otros shapes son null.
Históricos incompatibles con mapping/hash vigentes se separan, no se reproyectan.
Resultados y límites: [`h0012_auditoria_global.md`](../docs/h0012_auditoria_global.md).

Reporte sin GPS/IDs de unidades, con métricas null donde no hay pasajes.
Filtros iniciales experimentales 180 s / 100 m / 100 km/h. Detalles y resultados:
[`h0012_analisis.md`](../docs/h0012_analisis.md).

## Antigüedad GPS y ausencias H0012

Consultas rápidas read-only, sin PostGIS ni salida de ubicaciones/IDs de unidades:

```sh
psql "$DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 \
  -f scripts/audit-bottlenecks-gps-delay.sql > .local/h0012/gps-delay.json
psql "$DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 \
  -f scripts/audit-bottlenecks-update-gaps.sql > .local/h0012/update-gaps.json
TEST_DATABASE_URL="$DATABASE_URL" python3 scripts/test-bottlenecks-gps-delay.py
```

La primera mide antigüedad al recibir el GPS por primera vez, no latencia de red
pura. La segunda evalúa hipotéticamente cerrar tras varias capturas sin nuevos
timestamps: el raw canónico no permite reconstruir cada presencia repetida en
el feed. Los tests crean sólo fixtures TEMP de sesión y no escriben en `vialis`.
Decisiones y resultados: [`h0012_retraso_gps.md`](../docs/h0012_retraso_gps.md).

## Cobertura de pasajes H0012 por sector/fecha/hora

El comando de análisis también admite `-all`: usa los targets observados mediante
las equivalencias activas de H0011, sin confundir IDs API y GTFS. Presupuesto
global autorizado: 15 min; por target: 5 min. No cambia filtros GPS.

```sh
go run ./cmd/bottlenecks-analysis -all \
  -from '2026-10-08T17:01:10Z' -to '2026-10-11T00:46:52.000001Z' \
  > .local/h0012/coverage-retained.ndjson
python3 scripts/summarize-bottlenecks-coverage.py .local/h0012/coverage-retained.ndjson \
  > .local/h0012/coverage-retained-summary.json
python3 scripts/test-bottlenecks-coverage-summary.py
```

NDJSON con manifiesto, resultados por target y marcador `complete`. Sin ese
marcador la salida es parcial y el consolidador la rechaza. Franjas de una hora
local Buenos Aires; cada pasaje se atribuye por entrada estimada. No emite IDs de
unidades ni GPS individuales. Las entradas incompletas/censuradas no equivalen a
viajes reales perdidos; los cortes de volumen del resumen no son umbrales de
publicación. Resultados: [`h0012_cobertura_pasajes.md`](../docs/h0012_cobertura_pasajes.md).

## Auditoría de aglutinaciones

Snapshot consistente, sin escrituras, compatible con el worker en ejecución:

```sh
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -f scripts/audit-h0011-bunching.sql
```

## Backfill one-off de máximos de velocidad

`backfill-bunching-speeds.sql` completa los máximos de cada unidad desde raw
canónico de la misma identidad GCBA/GTFS, sentido y versión geométrica. Usa sólo
posiciones entre el comienzo del evento y el último GPS admitido de esa unidad,
no hasta el cierre. Excluye capturas parciales/no analizadas y posiciones fuera
de los límites de frescura (5 minutos, skew de 1 minuto).

```sh
# Preview: calcula cobertura y cambios, sin actualizar eventos (ROLLBACK).
psql "$DATABASE_URL" -X -f scripts/backfill-bunching-speeds.sql
# Aplicación explícita, transaccional y sin disminuir máximos existentes:
psql "$DATABASE_URL" -X -v apply=true -f scripts/backfill-bunching-speeds.sql
```

La aplicación comparte el advisory lock del worker/mantenimiento; puede hacerlos
esperar brevemente. Reporta filas actualizadas, cobertura de ambas unidades y
conteos elegibles antes/después con los defaults del motor (2 muestras, 15 minutos,
1 m/s por unidad). Sólo escribe las dos columnas de velocidad; no modifica
observaciones, tiempos, posiciones ni cierres. Puede reaplicarse sin disminuir
máximos; nuevos datos del worker pueden producir nuevos cambios.

**Es una aproximación histórica, no un replay exacto**: raw no conserva todos los
snapshots admitidos del par. Una posición guardada por primera vez en una captura
parcial se excluye aunque luego pudiera haberse reutilizado en una completa.
No inventa velocidades para períodos fuera de retención ni interpola muestras;
si falta evidencia, conserva el máximo existente (0 si nunca se registró).

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
