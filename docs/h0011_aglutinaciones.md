# H0011 — Aglutinaciones: decisiones, operación y API

Detecta pares cercanos del mismo scope operacional y conserva la evolución del
encuentro. No mide regularidad, frecuencia, pasajeros ni severidad: proximidad
puede deberse a terminales, detenciones o ruido GPS, no necesariamente a anomalías.
Es independiente del simulador de demanda, tiempo y recaudación.
**Pasos por parada, headways, CV y `/lines/{id}/regularity` fueron descartados**;
no son fases pendientes. Contrato HTTP: [`openapi.yaml`](openapi.yaml).

## Correspondencia GCBA–GTFS

El feed GCBA `vehiclePositionsSimple` y el catálogo GTFS usan namespaces distintos;
no vincular por nombre, `direction` o coincidencia de `trip_id` durante una request.
La tabla administrada `vialis.equivalencias_recorridos_gcba` resuelve
`gcba_route_id + gcba_direction → gtfs_route_id + gtfs_direction_id`.
Varias identidades GCBA pueden apuntar al mismo GTFS, pero no se mezclan al detectar
pares. Los IDs internos regenerables del catálogo no son claves históricas.

La selección versionada contiene 814 equivalencias: 248 candidatos fuertes y
566 ambiguos aceptados tomando el mayor score. No son 814 correspondencias de
confianza alta. Los casos sin vínculo se ignoran y cuentan como no vinculados;
la cobertura del catálogo limita qué líneas informa la API.

La selección compara muestras deterministas de hasta 500 y 800 observaciones por
identidad, con proximidad al shape, extensión cubierta y avance coherente.
La clasificación fuerte exige ≥100 posiciones, cobertura ≥90 % a 200 m y ≥70 %
a 100 m, 16/20 tramos observados, ≥30 pares temporales útiles, ≥80 % de avance,
margen de score ≥0,10 y mismo ganador en ambas muestras. Son criterios de selección
administrada, no filtros runtime. Se descartó vincular por `trip_id`: la auditoría
mostró baja compatibilidad espacial; compartir un ID no prueba el mismo viaje.

La fuente de `sql/recorridos/cargar_equivalencias_gcba.sql` es
[`h0011_spatial_match_candidates.csv`](h0011_spatial_match_candidates.csv).
Comandos de reproducción: [`scripts/README.md`](../scripts/README.md).
No cambiar equivalencias ni recargar GTFS desde HTTP. Un GTFS contemporáneo y
decoder protobuf son mejoras, no requisitos para mantener el detector actual.

## Componentes y estado

- `internal/transport/gcba`: cliente y validación del feed.
- `internal/realtime` / `cmd/realtime-worker`: polling, mantenimiento e ingesta.
- `internal/bunching`: ciclo de vida y servicios de lectura.
- `internal/database/postgres`: COPY, detección geográfica y consultas. El SQL
  extenso vive en archivos `.sql` adyacentes, embebidos con `go:embed` desde
  `bunching_queries.go` y `realtime_queries.go`, como en los demás repositorios.
  Cada sentencia documenta sus parámetros; no requiere archivos en runtime ni
  templates SQL. `find_bunching_recurrence.sql` contiene el agregado horario;
  `check_realtime_schema.sql`, la comprobación de arranque del worker. Los tests
  verifican que total/página históricos compartan filtros y las lecturas de
  eventos mantengan el orden de columnas de `scanEvent`.
- `internal/httpapi` / `cmd/api`: endpoints conectados al servicio de aglutinación.

Detector y endpoints están implementados. La migración se aplicó en la base del
proyecto y el worker produjo eventos reales. Para verificar un despliegue hay que
comprobar su versión y consultar su URL; el estado del código no acredita que un
binario antiguo haya sido actualizado.

## Preparación y ejecución

En bases existentes, aplicar las migraciones que correspondan **con el worker
detenido**, antes de cambiar sus binarios:

```sh
# Si aún no existen equivalencias/realtime:
psql "$DATABASE_URL" -f sql/recorridos/migrar_equivalencias_gcba.sql
psql "$DATABASE_URL" -f sql/recorridos/cargar_equivalencias_gcba.sql
psql "$DATABASE_URL" -f sql/recorridos/migrar_realtime.sql
# Esquema del detector y consulta histórica:
psql "$DATABASE_URL" -f sql/recorridos/migrar_aglutinaciones.sql
```

No ejecutar `cargar_equivalencias_gcba.sql` como mantenimiento rutinario: reemplaza
la tabla con el mapping versionado. Bases nuevas incluyen el esquema en
`sql/ddl.sql`; el orden de bootstrap pertenece a `internal/database/bootstrap`.
Las migraciones realtime no reemplazan el catálogo GTFS ni viajes/tarifas.

Exportar las variables sin imprimirlas; Go no carga `.env` automáticamente:

```sh
# Sólo si .env es compatible con shell:
set -a
. ./.env
set +a
mkdir -p .local
go build -o .local/realtime-worker ./cmd/realtime-worker
go build -o .local/api ./cmd/api
.local/realtime-worker -interval=1m
# En otra terminal con el entorno cargado:
.local/api
```

Worker: `DATABASE_URL`, `GCBA_TRANSPORT_CLIENT_ID`, `GCBA_TRANSPORT_CLIENT_SECRET`.
API: `DATABASE_URL`, `HTTP_ADDRESS` (default `:8080`) y `TOMTOM_API_KEY` requerido
por el servicio RF05 del mismo ejecutable; no consulta GCBA desde los handlers.
En el contenedor del proyecto la DB está en `vialis-motor-db:5432`.
Con direnv también puede usarse `direnv exec . .local/realtime-worker -interval=1m`.

**Ejecutar una sola instancia del worker.** No tiene lease distribuido; el advisory
lock de persistencia no convierte varios procesos en un único poller.
Los errores de inicio distinguen credenciales, conexión y esquema faltantes.

## Ciclo del worker

1. Comprueba credenciales, conexión y esquema del detector.
2. Mantiene particiones raw al inicio y al cambiar de fecha UTC; reintenta
   mantenimiento fallido en ciclos posteriores.
3. Captura inmediatamente `vehiclePositionsSimple`: cuerpo máximo 10 MiB,
   timeout de 120 s, hasta tres intentos, backoff acotado y `Retry-After`.
4. Valida y deduplica posiciones, resuelve equivalencias y confirma captura,
   raw y cambios de eventos en una sola transacción.
5. Espera el intervalo completo después de terminar captura + escritura.

No hay requests solapadas, ticker acumulativo ni cola durable para lotes fallidos.
Errores transitorios se registran y el siguiente ciclo vuelve a capturar.
Operaciones de DB limitadas a un minuto. SIGINT/SIGTERM cancelan requests,
backoff, transacciones y espera, y cierran el pool.

### Cobertura

`coverage_ratio` compara vehículos frescos contra la última captura no baja.
Una caída por debajo de 0,7 emite `coverage_warning`; no sustituye la referencia.
Cero frescos o cobertura baja identificada marca la captura parcial.
La referencia vive en memoria: no detecta necesariamente una captura parcial
inicial, caídas graduales o variaciones naturales del servicio. Es una heurística.

## Persistencia y retención

- `capturas_colectivos`: conteos, recepción, fingerprint, cobertura y análisis.
- `posiciones_colectivos_raw`: sólo posiciones con equivalencia y destino GTFS
  existente; guarda también stale dentro de la retención. PK por fecha UTC,
  vehículo y timestamp; COPY por lote, sin duplicar observaciones entre capturas.
- `aglutinaciones_colectivos`: par canónico, identidad versionada, nombres,
  tiempos individuales, confirmación, distancia/posiciones, muestras y cierre.
- `pasos_colectivos`: tabla preparatoria existente, **sin inferencia ni uso**;
  pasos/headways/CV fueron descartados. No se elimina mediante esta decisión.

El worker conserva **indefinidamente** las particiones raw y los metadatos de
capturas: `config.RealtimePreserveHistory = true` desactiva su purga automática.
El mantenimiento sigue creando las particiones de la ventana de admisión y
mañana. No recupera datos ya eliminados. Hay que monitorear disco/WAL y administrar
respaldos; el crecimiento no tiene un límite automático.

La admisión de GPS entrante sigue limitada a hoy y seis fechas UTC anteriores
(`RealtimeRawRetentionDays = 7`); siete fechas no son exactamente 168 horas.
No cambia la frescura del detector ni se hace backfill. Si se cambia explícitamente
`RealtimePreserveHistory` a false y se recompila/reinicia, vuelve la purga:
se eliminan particiones completas fuera de esa ventana y capturas viejas sin
posiciones referenciadas. Mantenimiento e inserción comparten un lock transaccional.

Las identidades históricas no tienen FK a IDs regenerables del catálogo. Conservan
ruta/sentido GCBA y GTFS, shape_id y `md5(encode(ST_AsEWKB(geom),'hex'))`.
El hash distingue versiones geométricas; no archiva el shape completo ni es un
mecanismo de seguridad. La historia no desaparece tras una recarga.

No hay backfill de eventos anteriores al detector: raw deduplicado no permite
reconstruir la pertenencia completa a cada respuesta sin inventar ausencias.

**Eventos no se purgan físicamente todavía.** Consultar hasta 30 días no borra
filas antiguas. Esa retención sigue como pendiente operacional independiente de
la regularidad descartada. Dimensionar disco/WAL con datos reales: el benchmark
sintético de un millón de posiciones ocupó ~300,6 MB con índices, no es una
estimación fiable de filas diarias ni incluye WAL/backups.

## Detector y ciclo de vida

- Selecciona la última observación de cada vehículo del lote completo, incluso si
  ya existía en raw. Usa coordenadas canónicas del vehículo/timestamp persistido.
- Sólo analiza GPS de hasta cinco minutos, con skew futuro máximo de un minuto.
- Separa GCBA ruta/sentido **y** destino GTFS/dirección/versión; identidades GCBA
  distintas no se mezclan aunque compartan destino.
- Busca con GiST/geography y mide con `ST_Distance`; umbral inclusivo <=100 m
  (`config.BunchingDistanceMeters`). El prefilter se amplía un micrómetro para
  no perder el borde, pero la distancia final se filtra estrictamente al radio
  configurado. El worker debe recompilarse/reiniciarse para cambiar el radio.
  Los eventos históricos conservan lo observado con el radio anterior (200 m);
  cambiar esta constante no reconstruye ni elimina esas series.
- Un avistamiento abre candidato de una muestra. Se confirma desde dos
  observaciones; para incrementarlas avanzan **ambos** timestamps GPS.
  **La API sólo publica encuentros de al menos 15 minutos de duración GPS**,
  mediante `config.BunchingMinimumDuration` en `internal/config/parameters.go`.
  El umbral es inclusivo y aplica al estado actual, histórico, total y paginación.
  También exige que **ambas unidades hayan alcanzado >=1 m/s (3,6 km/h)** al menos
  una vez en las muestras admitidas del encuentro. Se persisten los máximos en
  `velocidad_maxima_a_metros_segundo` y `velocidad_maxima_b_metros_segundo`,
  inicializados en la apertura y actualizados sólo en confirmaciones con ambos
  timestamps nuevos, dentro del radio, misma identidad y GPS frescos. Se usa la
  velocidad canónica de raw, no correcciones posteriores del mismo timestamp.
  Capturas parciales, muestras repetidas y posiciones de cierre por separación
  no actualizan máximos; éstos nunca disminuyen dentro del evento. Cada unidad
  puede alcanzar su máximo en muestras distintas. No exige movimiento sostenido.
  No retrasa la captura ni borra los encuentros cortos; cero deshabilita el filtro.
  No se cuenta tiempo desde ahora ni hasta el cierre para alcanzar el mínimo.
  `config.BunchingMinimumSpeedMetersPerSecond` controla el umbral de movimiento;
  cero deshabilita sólo ese filtro. Cambiar umbrales requiere recompilar/reiniciar
  la API, sin modificar los máximos persistidos ni reiniciar el worker.
  Al incorporar las columnas sí debe migrarse la base y actualizarse el worker.
  No hay backfill automático: registros anteriores reciben máximos 0 y no pasan
  el umbral de movimiento hasta que haya nuevas muestras admitidas que lo acrediten.
  Para recuperar evidencia histórica de raw existe el one-off administrado
  `scripts/backfill-bunching-speeds.sql` (preview por defecto, aplicación explícita).
  Sus límites y comandos están en [`scripts/README.md`](../scripts/README.md):
  es una aproximación conservadora, no un replay exacto de las muestras del detector.
- Pares independientes: A–B y B–C no implican A–C ni un grupo transitivo.
- Respuestas repetidas no suman muestras ni duración ni confirmación. Fingerprint
  del proveedor completo, incluidos vehículos no mapeados; cero inserts no basta
  para decidir repetición. Pueden expirar eventos por falta de evidencia nueva.
- Capturas parciales conservan raw/metadata, pero no modifican eventos.
- Un par cerrado no reinicia otra serie con los mismos timestamps ya usados.

Motivos de cierre:

| Motivo | Evidencia |
|---|---|
| `separated` | Ambos presentes/frescos, timestamps nuevos y distancia >100 m (radio configurado) |
| `missing` | Falta una unidad en dos revisiones completas distintas; tolera una |
| `expired` | Más de cinco minutos sin recepción confirmatoria o hueco de GPS >5 min |
| `identity_changed` | Una unidad cambia de identidad/versión con un timestamp nuevo |

Falta y expiración **no prueban separación física**. El cierre por expiración
queda en el límite de evidencia, no en el retorno tardío del worker, y nunca
precede la última observación admitida.

El instante del par usa el GPS más reciente de sus componentes. La duración
pública es último menos primer GPS admitido, no tiempo desde ahora ni hasta el
cierre. Posiciones/distancias históricas son las últimas dentro del encuentro,
no las posteriores a su separación. Los tiempos GPS y de recepción se distinguen
para no cortar secuencias sólo porque el GPS llegue demorado.

Una fila abierta puede quedar obsoleta durante una caída: `open` no equivale a
`active=true`. Las consultas usan read-only/repeatable-read para no mezclar commits.

## Endpoints

Para presentar capturas existentes sin que envejezcan, `DEMO_MODE=true` al
iniciar `cmd/api` (o el flag `-demo`) fija
«ahora» en la última captura analizada al arrancar, tanto para frescura como
para ventanas por defecto y recurrencia. No modifica fechas ni reconstruye
estados pasados; requiere conservar el estado final del worker sin nuevas
escrituras/purgas. Comandos y límites en [operación de demo](../README.md#demo-con-capturas-existentes).

```sh
curl 'http://localhost:8080/lines/123/bunching'
curl 'http://localhost:8080/bunching/recurrence'
curl 'http://localhost:8080/bunching?state=all&limit=50&offset=0'
curl 'http://localhost:8080/bunching?state=closed&from=2026-10-08T21:00:00Z&to=2026-10-08T23:00:00Z'
```

`123` es un **id_recorrido de `GET /lines`**, no el número público de línea.

### `GET /lines/{id}/bunching`

Estado actual del recorrido: línea/ramal/sentido, `mapped`, `latestCaptureAt`,
`captureAgeSeconds`, `partial`, `stale` y `pairs`. No acepta query parameters.
Sin captura analizada, parcial o stale: `pairs: []` con indicadores, no una
aseveración de que no existen encuentros. Con datos recientes, los pares de menos
de 15 minutos también quedan fuera. `confirmationPending` señala la revisión
faltante tolerada. Los pares publicados también requieren máximos >=1 m/s en
ambas unidades. Incluye `recurrence` con el resumen semanal aun si `pairs` está
vacío por falta de captura reciente. ID inválido: 400; recorrido inexistente: 404.

### `GET /bunching/recurrence`

Resumen por **ruta GTFS y sentido** de los encuentros observados en las últimas
168 horas, desglosados por **franjas locales fijas de una hora** (00:00–01:00,
01:00–02:00, …, 23:00–24:00) en `America/Argentina/Buenos_Aires`. Una franja es
recurrente si hubo encuentros en ella en **dos o más fechas distintas**. El
`recurrent` del recorrido es true si alguna franja lo es, no simplemente por tener
eventos en varios días. Ventana `[from,to)`, donde `to` es ahora y `from` es ahora
menos siete días. Al ser móvil, puede tocar partes de ocho fechas locales.

- Cuenta eventos con al menos dos observaciones, duración GPS >=15 minutos y
  máximos de velocidad de **ambas unidades >=1 m/s**, usando la misma política
  de publicación que las consultas actuales/históricas.
- Intersecta la evidencia `[firstObservedAt,lastObservedAt)` con la ventana de
  consulta y cada franja. Un evento de 08:50 a 09:20 cuenta en las franjas 8 y 9;
  si termina exactamente a las 09:00, sólo cuenta en la franja 8. No se extiende
  hasta `closedAt` ni hasta ahora. Eventos iniciados antes de la semana sí cuentan
  por la parte observada dentro de ella; la duración mínima se evalúa completa.
- Al cruzar medianoche, cada franja lleva la fecha local que corresponde. Incluso
  un evento de varios días puede aportar fechas distintas a la misma franja,
  pero se cuenta una sola vez por franja y en el total semanal.
- Agrega vehículos, zonas, identidades GCBA y versiones geométricas distintas del
  mismo destino GTFS/sentido. No mezcla sentidos ni cambia el detector por pares.
- Expone `hours`, sólo con las franjas con evidencia, ordenadas por `hour` (0–23).
  Cada una trae `eventCount` (eventos únicos), `daysWithEvents`, `dates` locales
  únicas/ordenadas y `recurrent`. Los mismos conteos a nivel del recorrido son
  totales únicos de toda la semana: **no se suman** los conteos de las franjas,
  porque un evento puede aparecer en varias. También incluye `from`, `to`,
  `timeZone` y `minimumDays` efectivos.

  Ejemplo de una franja recurrente de 08:00 a 09:00:

  ```json
  {"hour":8,"eventCount":3,"daysWithEvents":2,"dates":["2026-10-08","2026-10-09"],"recurrent":true}
  ```

- `lines` incluye sólo recorridos con al menos un evento elegible en la ventana,
  incluidos históricos ya fuera del catálogo (`lineId: null`). No exige que el
  encuentro siga activo ahora ni que sea recurrente. Orden por `eventCount`
  descendente, con desempate por `gtfsRouteId`, `direction` ascendentes;
  sin paginación ni query parameters.
  Los nombres del catálogo prevalecen; sin catálogo se usan los del último evento.
- `mapped` describe la equivalencia vigente, no la cobertura de la semana.
  **Cero eventos o `recurrent: false` no prueban ausencia de aglutinaciones**:
  pueden faltar días de captura. No hay backfill ni inferencia durante caídas.
  La recurrencia no mide frecuencia, regularidad, severidad ni pasajeros.

`GET /lines/{id}/bunching` incluye el mismo resumen en `recurrence` para el
recorrido solicitado, incluso si tiene cero eventos (`hours: []`). Estado actual
y resumen se leen en snapshots independientes
con un mismo corte temporal; una captura concurrente puede verse en uno y no en
el otro. Los flags `stale`/`partial` siguen describiendo sólo el estado actual.

Los parámetros están en `config.BunchingRecurrencePolicy()`. Para bases existentes,
reaplicar `sql/recorridos/migrar_aglutinaciones.sql` agrega de forma idempotente el
índice por inicio, complementario al índice existente por última observación
para la intersección temporal; no cambia ni reconstruye eventos.

### `GET /bunching`

Eventos confirmados globales, abiertos e históricos:

- `from`, `to`: RFC3339; por defecto últimas 24 horas; máximo 30 días por intervalo.
- `state`: `all` (default), `open` o `closed`.
- `limit`: default 50, máximo 200; `offset` no negativo.
- Rechaza filtros desconocidos, duplicados o inválidos con 400.

La ventana [from,to) intersecta inicio hasta cierre; para abiertos, hasta última
observación, sin extender evidencia obsoleta hasta infinito. Orden estable:
`firstObservedAt DESC, id DESC`. Incluye total y filtros efectivos; no lista
candidatos de una única muestra ni encuentros de duración GPS menor al mínimo
configurado (15 minutos por defecto), ni encuentros donde alguna unidad no
alcanzó el mínimo de movimiento (1 m/s por defecto). Los eventos publicados
exponen `maximumSpeedAMetersPerSecond` y `maximumSpeedBMetersPerSecond`.

Cada evento expone scope, nombres, vehículos, posiciones/punto representativo,
distancias mínima/última, muestras, duración, estado, `closedAt`, `closeReason` y
`active`. Actividad exige captura reciente no parcial, versión vigente, ambos
GPS frescos y ninguna confirmación pendiente. Los indicadores del envelope
reflejan el estado actual, no toda la calidad del período histórico.

Tras reemplazar catálogo/equivalencia, `lineId` puede ser null; se conservan
nombres, claves y hash originales. Un handler sin servicio configurado devuelve
503 `bunching_unavailable`; `cmd/api` conecta el servicio normalmente.

## Logs y comprobaciones

Logs JSON: inicio/parada, mantenimiento, captura almacenada/fallida, escritura
fallida y errores de configuración. Incluyen conteos totales/válidos/inválidos,
duplicados/stale, no vinculados, insertados, cobertura, duración, capture_id,
`bunching_processed`, `bunching_skipped`, eventos abiertos/actualizados/cerrados.
No registran URLs autenticadas, credenciales ni textos arbitrarios del proveedor
o driver. No hay servidor Prometheus ni endpoint HTTP del worker.

```sh
go test ./...
# Opt-in contra PostGIS real; tests usan schemas aislados y rollback:
TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/database/postgres \
  -run 'TestBunching|TestRealtime' -count=1 -v
# Auditoría consistente sin escrituras, mientras el worker sigue corriendo:
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -f scripts/audit-h0011-bunching.sql
```

Los tests realtime/aglutinaciones comparten `realtime_fixture_test.go`: conexión,
contexto, transacción y schema aislado, con rollback mediante `t.Cleanup` antes de
cerrar el pool. Cada test aplica explícitamente las migraciones que necesita;
los casos legacy y de idempotencia conservan su orden y repeticiones. No se
comparten datos entre fixtures ni se modifica el schema `vialis` del proyecto.

Las pruebas cubren cliente defensivo, recuperación/shutdown, particiones,
idempotencia, borde geodésico 199,99/200/200,01 m, confirmación, repeticiones,
stale/parciales, cierres, recarga de catálogo, filtros/paginación y errores HTTP.

La auditoría inicial del 08/10 encontró cero inconsistencias de distancia,
timestamps y orden de pares. En las capturas 178/179 se verificó el mismo
fingerprint, cero nuevas filas y ningún aumento de muestras. Estos controles
comprueban coherencia del detector, **no que cada encuentro sea una anomalía**.
El contraste operacional, la retención física de eventos, el dimensionamiento
sostenido de disco/WAL y la cuota de polling siguen pendientes.
