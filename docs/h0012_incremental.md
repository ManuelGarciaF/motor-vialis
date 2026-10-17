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

## Matching y tardíos: límite explícito

Esta pieza no consume directamente el raw ni decide qué matching es final.
Los candidatos ambiguos pueden requerir evidencia posterior; pasarlos como
barreras definitivas en cada fin de lote perdería información válida.

Una observación anterior al watermark activo retorna
`ErrObservationBeforeWatermark`, sin descartarla silenciosamente ni avanzar el
checkpoint. El procesador deberá resolver buffering y orden de GPS antes de
confirmar transiciones, dentro de la retención raw. El replay de un timestamp
igual al watermark se acepta sólo si la observación coincide; un conflicto
rechaza la captura completa.

Por lo tanto esta implementación **no acredita paridad end-to-end de matching
batch/incremental** ni resuelve todavía las correcciones dentro de secuencias
abiertas. La paridad probada es del núcleo de medición para entradas ya resueltas
con orden y política equivalentes.

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

Pendiente antes de activar H0012:

1. Lectura incremental, matching provisional, buffering y tratamiento de cambios
   de catálogo, conservando sólo el contexto operativo necesario.
2. Productor de transiciones que entregue evidencia completa y respete capturas
   reales, sin convertir fines de lote en censura ni confirmar matching prematuro.
3. Ciclo independiente, cancelación y apagado coordinados en el worker.
4. Validación end-to-end batch/incremental y medición del costo para elegir
   frecuencia. Ni el batch completo ni ventanas independientes reemplazan esto.

La migración no se aplicó al esquema local `vialis` ni se activó un modelo real.
