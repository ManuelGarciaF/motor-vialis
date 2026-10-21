# H0012 — Estado incremental de medición

Plan funcional: [plan_h0012_cuellos_botella.md](plan_h0012_cuellos_botella.md).
Regla de ausencia: [h0012_retraso_gps.md](h0012_retraso_gps.md).

## Decisiones confirmadas

- Ciclo H0012 independiente en el mismo proceso `realtime-worker`, sin bloquear
  la ingesta ni ejecutar medición dentro de su transacción.
- Cierres por cuatro capturas correctas no parciales sin timestamp nuevo,
  definitivos: no reabrir la secuencia ni completar su entrada con GPS posterior.
- Pasajes y metadatos sin purga inicial. Sin archivo adicional de series GPS.
- Activación con datos nuevos, sin backfill. La caída del proveedor no cambia
  los acuerdos ni justifica procesar retrospectivamente el backup.

## Componente de medición implementado

`internal/bottlenecks/incremental.go` incorpora `MeasureCapture`: transición pura
para **una unidad, layout y política**, sobre observaciones ya resueltas por
matching. `Measure` y esta transición comparten `measurementCursor`, por lo que
la interpolación, los cortes de continuidad y la medición comercial usan el
mismo núcleo. No se fijan defaults de producción.

`CaptureState` incluye identidad, configuración inmutable, checkpoint de captura,
watermark GPS, última observación resuelta admisible y entrada pendiente con
soporte y contadores de calidad. Se puede serializar con JSON sin coordenadas
GPS ni una serie histórica de ubicaciones. La progresiva de continuidad es
estado operativo mínimo, no un archivo de trayectorias.

Cada llamada procesa una captura real en orden, no una ejecución del scheduler:

1. Valida estado, identidad, metadatos y conflictos antes de emitir resultados.
2. Procesa timestamps nuevos antes de contar ausencia. Coordenada/progresiva
   idéntica con timestamp posterior reinicia el contador.
3. Las capturas parciales pueden aportar observaciones, pero no sumar ausencias.
   Fallos no aportan observaciones ni suman ausencias.
4. Al alcanzar cuatro ausencias elegibles, emite la entrada pendiente como
   incompleta (`missing_updates`), sin salida ni duración inventadas. El contador
   satura y el cierre no se repite.
5. GPS posterior puede empezar otra secuencia independiente. Evidencia anterior
   al watermark de una secuencia cerrada se cuenta como tardía y no la reabre.

Los IDs de captura repetidos retornan `ErrCaptureAlreadyProcessed` sin emisiones.
Un error no entrega estado avanzado ni resultados parciales; la entrada no se
muta. Este contrato **no sustituye una transacción DB**: resultados y checkpoint
se deben confirmar atómicamente para conseguir idempotencia entre reinicios.

`NewCaptureState` recibe el ID de captura de activación; ese valor debe provenir
de una marca global persistida, no recalcularse al reiniciar o descubrir unidades.
El estado no debe reconstruirse con un checkpoint cero después de una caída.

## Cursor resuelto y productor operacional

`MeasureCapture` no consume directamente raw ni decide qué matching es final.
Los candidatos ambiguos pueden requerir evidencia posterior; pasarlos como
barreras definitivas en cada fin de lote perdería información válida. Por eso
el [productor](h0012_productor.md) usa `ProducerState`/`ProduceCapture`, separados
del cursor resuelto: cuenta recepción real y confirma mediciones sólo al cierre.

Una observación anterior al watermark activo retorna
`ErrObservationBeforeWatermark`, sin descartarla silenciosamente ni avanzar el
checkpoint. El productor conserva descriptores de la secuencia abierta y relee
su raw canónico al cierre, dentro de la retención, sin archivar series GPS. El replay de un timestamp
igual al watermark se acepta sólo si la observación coincide; un conflicto
rechaza la captura completa.

La paridad del cursor se valida sobre observaciones ya resueltas. La paridad
del productor se compara con el matcher y medición batch sobre la misma evidencia
aceptada y cerrada, incluyendo tardíos y barreras de scope en orden GPS; no con
una ventana histórica arbitraria que termina censurada. No hay resultados
provisionales publicados ni un prefijo irreversible de una secuencia abierta.

## Validación y trabajo pendiente

Tests de dominio cubren:

- Entradas entre capturas sin censura de lote y recuperación JSON del estado.
- Todas las 128 particiones de una trayectoria de ocho observaciones, con
  reinicio entre lotes y resultado igual al batch.
- Actualización en la cuarta captura, detenciones, parciales, errores y cierre
  único; no se amplía el máximo hueco GPS.
- Cambios de identidad, replays, tardíos, conflictos y validación del estado.
- Paridad por particiones con cortes por ambigüedad, hueco GPS, retroceso,
  cambio de identidad y velocidad implausible.

La [capa de persistencia](h0012_persistencia.md) implementa snapshots de geometría,
sectores y paradas, mediciones e incompletos, diagnósticos y estado/checkpoint
transaccionales. La activación es explícita, sin backfill; los resultados no
referencian el catálogo reemplazable ni raw sujeto a purga. Las asociaciones de
paradas a sectores conservan candidatos, no decisiones arbitrarias en lazos.

Validación local: `go test ./...`, `go build ./...` y `go vet ./...` pasaron.
Los tests PostGIS `TestPassageStore*` pasaron en esquemas aislados, incluidos
rollback, recuperación, concurrencia y retención. El race detector no pudo
ejecutarse: este entorno Windows tiene CGO deshabilitado y no dispone de GCC.

El productor PostgreSQL y su ciclo independiente opt-in están implementados.
Las pruebas agregan fuente canónica, finalización atómica, reinicio, modo de
procesamiento, carreras con purga y lifecycle del worker. No se soporta recargar
GTFS mientras corre el proceso; si falta un snapshot exacto, H0012 se detiene.

Antes de operar: aplicar explícitamente la migración adicional del productor,
seleccionar parámetros experimentales/cadencia y activar un modelo sin backfill.
La migración inicial ya se aplicó a `vialis`; la adicional y la activación real
siguen pendientes. Falta medir costo e impacto sobre captura y validar operación
con GCBA disponible. Comandos y límites: [productor](h0012_productor.md).
