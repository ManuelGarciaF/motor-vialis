# H0011 — operación y API de aglutinaciones

Alcance y decisiones: [`plan_h0011_aglutinacion.md`](plan_h0011_aglutinacion.md).
Contrato HTTP completo: [`openapi.yaml`](openapi.yaml).

## Componentes y estado

- `internal/transport/gcba`: cliente y validación del feed.
- `internal/realtime` / `cmd/realtime-worker`: polling, mantenimiento e ingesta.
- `internal/bunching`: ciclo de vida y servicios de lectura.
- `internal/database/postgres`: COPY, detección geográfica y consultas.
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

Raw conserva hoy y seis fechas UTC anteriores, y precrea mañana. La retención
elimina particiones completas, no grandes `DELETE`; siete fechas no son
exactamente 168 horas. Capturas viejas se eliminan cuando no quedan posiciones
que las referencien. Mantenimiento e inserción comparten un lock transaccional.

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
- Busca con GiST/geography y mide con `ST_Distance`; umbral inclusivo <=200 m.
  El prefilter se amplía un micrómetro para no perder el borde, pero la distancia
  final se filtra estrictamente a 200 m.
- Un avistamiento abre candidato de una muestra. Se confirma desde dos
  observaciones; para incrementarlas avanzan **ambos** timestamps GPS.
  **La API sólo publica encuentros de al menos 15 minutos de duración GPS**,
  mediante `config.BunchingMinimumDuration` en `internal/config/parameters.go`.
  El umbral es inclusivo y aplica al estado actual, histórico, total y paginación.
  No retrasa la captura ni borra los encuentros cortos; cero deshabilita el filtro.
  No se cuenta tiempo desde ahora ni hasta el cierre para alcanzar el mínimo.
  Cambiarlo requiere recompilar/reiniciar la API, sin migración ni reinicio del worker.
- Pares independientes: A–B y B–C no implican A–C ni un grupo transitivo.
- Respuestas repetidas no suman muestras ni duración ni confirmación. Fingerprint
  del proveedor completo, incluidos vehículos no mapeados; cero inserts no basta
  para decidir repetición. Pueden expirar eventos por falta de evidencia nueva.
- Capturas parciales conservan raw/metadata, pero no modifican eventos.
- Un par cerrado no reinicia otra serie con los mismos timestamps ya usados.

Motivos de cierre:

| Motivo | Evidencia |
|---|---|
| `separated` | Ambos presentes/frescos, timestamps nuevos y distancia >200 m |
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

```sh
curl 'http://localhost:8080/lines/123/bunching'
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
faltante tolerada. ID inválido: 400; recorrido inexistente: 404.

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
configurado (15 minutos por defecto).

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

Las pruebas cubren cliente defensivo, recuperación/shutdown, particiones,
idempotencia, borde geodésico 199,99/200/200,01 m, confirmación, repeticiones,
stale/parciales, cierres, recarga de catálogo, filtros/paginación y errores HTTP.

La auditoría inicial del 08/10 encontró cero inconsistencias de distancia,
timestamps y orden de pares. En las capturas 178/179 se verificó el mismo
fingerprint, cero nuevas filas y ningún aumento de muestras. Estos controles
comprueban coherencia del detector, **no que cada encuentro sea una anomalía**.
El contraste operacional y la retención física siguen pendientes.
