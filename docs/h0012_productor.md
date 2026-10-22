# H0012 — Productor de cuellos de botella en el worker

El productor se ejecuta siempre dentro de `realtime-worker`, en un ciclo y pool
independientes de la captura H0011. No necesita flags de habilitación, nombres de
experimentos ni una activación manual. Sigue siendo medición experimental, no un
detector de lentitud recurrente. Referencias: [plan](plan_h0012_cuellos_botella.md),
[procesamiento incremental](h0012_incremental.md) y [persistencia](h0012_persistencia.md).

## Ejecución

Exportar `DATABASE_URL`, `GCBA_TRANSPORT_CLIENT_ID` y
`GCBA_TRANSPORT_CLIENT_SECRET`; no se carga `.env` automáticamente.

```sh
go run ./cmd/realtime-worker
```

Este comando inicia captura, aglutinaciones y medición de pasajes. Para pruebas,
apuntar `DATABASE_URL` a una copia: la captura administra retención raw y puede
purgar histórico vencido. No ejecutar simultáneamente otro poller de GCBA.

Parámetros, versiones y recursos están en `internal/config/parameters.go`:
500 m / 180 s / 100 m / 100 km/h, espera de 2 minutos entre ciclos, pool de dos
conexiones y turnos de hasta 32 capturas o cinco minutos. Son valores
experimentales, no una cadencia calibrada ni umbrales de publicación.

## Inicialización y reinicios

Antes de iniciar captura, el worker verifica el esquema y busca su configuración
operacional única (`config.BottlenecksModel()`). El nombre interno estable sólo
sirve para recuperar estado y checkpoint; no lo elige el usuario.

Si no existe, la inicializa automáticamente excluyendo capturas ya almacenadas:
**sin backfill**. En reinicios recupera configuración y checkpoint originales,
sin volver a activarlos ni cambiar la marca inicial. Se compara toda la política
persistida con config; incompatibilidades o fallos DB no se interpretan como una
configuración ausente y no provocan resets. Un cambio de política sobre datos
existentes requiere administración explícita, no otro flag.

El worker no crea ni migra tablas. `sql/ddl.sql` define las cinco tablas
operacionales; en una base existente, las actualizaciones se administran con el
worker detenido. No ejecutar todo el DDL sobre una base poblada.

## Recepción y confirmación

Mientras una secuencia está abierta, guarda sólo un descriptor: unidad,
referencias a capturas/layouts/scopes, límites GPS, contador y fingerprint. No
archiva listas GPS, coordenadas ni candidatos históricos adicionales.

Un timestamp nuevo reinicia ausencias aunque la unidad esté quieta o sin matching.
Capturas parciales aportan actualizaciones, pero no suman ausencias. Errores del
proveedor, ticks, reintentos e inactividad no son capturas ni ausencias.
Tras cuatro capturas correctas no parciales sin timestamp más nuevo, se relee el
raw aceptado, se verifica conteo/fingerprint y se ordena por tiempo GPS.

El matcher usa las alternativas originales. Cambios de identidad, sentido o
versión son barreras: A→B→A no se une. GPS tardío dentro de OPEN puede cambiar la
reconstrucción; después del cierre no se reabre. Las entradas sin salida quedan
incompletas, sin inventar velocidad ni duración hasta la hora del cierre.

No se publican prefijos provisionales. Una unidad que actualice continuamente
puede demorar resultados hasta agotar retención. Si falta raw necesario o la
geometría exacta, el productor se detiene con aviso, sin truncar contexto, forzar
cierres, hacer backfill ni saltar el checkpoint. H0011 continúa.

## Transacción y recursos

`BottlenecksStore.ProcessNext` confirma una captura por transacción:

1. Bloquea configuración/checkpoint y valida retención.
2. Fija padre y particiones raw con ACCESS SHARE; revalida la referencia original
   después del lock, incluso al cruzar medianoche UTC.
3. Verifica filas canónicas contra `filas_insertadas` y usa la identidad/versiones
   del raw, sin reinterpretarlas con mappings actuales.
4. Avanza todos los estados de recepción y rematchea/mide sólo las secuencias que
   cierran, con su evidencia canónica completa.
5. Confirma mediciones finales, estado y checkpoint atómicamente.

Los conteos de avance, calidad y rechazos se registran en logs después de commits,
no en otra tabla. La calidad de cada pasaje permanece en su medición.

La proyección prepara una tabla TEMP de aristas originales por captura, con índices
por trazado y `geography`. Sólo contiene geometría estática; se elimina al commit
(o rollback) y se reutiliza para todos los cierres de esa captura. `ANALYZE` se
ejecuta una sola vez sobre esa tabla, nunca por unidad ni sobre tablas operativas.
Los cálculos geodésicos y las alternativas de matching no cambian.

La retención OPEN se comprueba mediante el timestamp más antiguo, antes y después
de las transiciones. Los estados se guardan con un único UPSERT por captura;
mediciones, estados y checkpoint siguen siendo atómicos. Cada unidad recibe sólo
sus propios layouts, sin reconstruir el catálogo completo en cada transición.

El pool se identifica como `application_name=vialis-bottlenecks` para inspeccionar
`pg_stat_activity`. El log `bottlenecks_captures_committed` incluye `duration_ms`
del turno, sin incluir la espera entre ciclos.

El pin raw permite INSERT normales, pero demora la purga. El pool separado no
aísla CPU, IO ni locks de mantenimiento: medir impacto sobre captura y DB.
No recargar GTFS mientras corre el worker; detenerlo antes de actualizar catálogo.

El ciclo es serial, sin solapamientos. Fallos transitorios se reintentan en el
siguiente turno. Pérdida/expiración de fuente, geometría ausente o política
incompatible detienen sólo el productor con `bottlenecks_processing_stopped`.
Errores de esquema/configuración al arrancar impiden iniciar el worker.
Cancelación o fin de ingesta cancela y espera el productor antes de cerrar pools.
Logs sin GPS, IDs de unidades, URLs autenticadas ni textos arbitrarios de drivers.

### Medición de la optimización

Comparación puntual sobre la captura real 900, con una muestra determinista de
500 posiciones, en el mismo snapshot. Se leyó producción sin cambiar checkpoints,
estados ni mediciones; las únicas escrituras fueron TEMP y se hizo rollback.

| Trabajo | Tiempo observado |
|---|---:|
| Proyección anterior, 500 posiciones | 4.836 ms |
| Proyección indexada, mismas 500 posiciones | 61 ms |
| Preparar 226.720 aristas e índices + ANALYZE, una vez | ~1.925 ms |
| Proyección indexada, captura completa de 3.624 posiciones | 326 ms |

Los resultados de las 500 posiciones dieron el mismo hash, incluyendo identidad,
timestamps y todas las alternativas/distancias. El plan nuevo usa BitmapAnd de
los índices espacial y de trazado. La preparación es un costo adicional por
captura: no interpretar la diferencia de las consultas como mejora equivalente
del worker completo. Estas cifras se midieron con el worker anterior activo;
falta validar CPU/IO y tiempos totales después de reiniciarlo con el código nuevo.
No requiere migración, cambio de parámetros ni reset de estado.

## Validación y pendientes

```sh
go test ./...
go build ./...
go vet ./...
TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/database/postgres \
  -run '^TestProducerStore|^TestBottlenecks' -count=1
```

Tests de arranque verifican inicialización automática sólo cuando falta la
configuración, recuperación sin reset y rechazo de incompatibilidades. Las
pruebas de loop usan fakes, sin llamar al proveedor. Dominio/PostGIS cubren
particiones/reinicios, tardíos, ambigüedad, A→B→A, cierres reales, rollback,
concurrencia y purga intercalada, incluyendo DDL completo y soporte de calidad.

El [corte operacional del 21/10/2026](h0012_estado_operacional.md) verificó
avance y pasajes persistidos, con atraso de cero/una captura durante la inspección.
Falta validar costo/cadencia e impacto sostenidos con capturas reales. La
[detección experimental](h0012_deteccion.md) implementa la primera lectura HTTP
con ≥2 pasajes a <6 km/h. No hay todavía criterios definitivos calibrados,
consolidación territorial ni ranking.
