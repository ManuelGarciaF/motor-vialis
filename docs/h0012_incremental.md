# H0012 — Procesamiento incremental

Referencias: [plan funcional](plan_h0012_cuellos_botella.md),
[regla de ausencia](h0012_retraso_gps.md), [persistencia](h0012_persistencia.md)
y [operación del productor](h0012_productor.md).

## Un único camino operacional

`ProcessNext` lee una captura canónica real; `ProduceCapture` es la transición
pura de recepción por unidad. No hay otro cursor resuelto ni selección de modos.
El antiguo `MeasureCapture` y su persistencia fueron retirados: conservar dos
caminos obligaba a almacenar estado duplicado y coordinar mecanismos que el
worker no necesitaba.

El estado mínimo es `ProducerState`: configuración, unidad, checkpoints,
timestamps de recepción/GPS, watermark cerrado, contador de ausencias y descriptor
OPEN. Este último conserva referencias a capturas/layouts/scopes y un conteo y
fingerprint de evidencia. No guarda listas GPS ni candidatos históricos.

## Recepción y cierre

Cada transición procesa una captura real, no un tick del scheduler:

1. Procesa actualizaciones antes de sumar ausencias. Un timestamp más nuevo
   reinicia el contador, incluso con coordenadas iguales o sin matching.
2. Capturas parciales aportan novedades, pero no suman ausencias. Errores del
   proveedor tampoco suman.
3. Tras cuatro capturas correctas no parciales sin timestamp nuevo, cierra la
   secuencia y relee toda su evidencia canónica aceptada.
4. Ordena por tiempo GPS, ejecuta el matcher sobre alternativas originales y mide
   pasajes con el mismo núcleo batch. Los cambios A→B→A son barreras, no se unen.
5. Confirma resultados finales una sola vez. Entradas sin salida son incompletas
   por `missing_updates` o cambio de scope, sin extrapolar duración ni lentitud.

GPS tardío dentro de OPEN puede cambiar matching e interpolación al cierre. Tras
cerrar, no se reabre ni corrige evidencia; GPS más nuevo puede iniciar otra
secuencia. El máximo hueco de soporte de 180 s experimental no cambia por usar
cuatro capturas de cierre.

## Persistencia y límites

Configuración/checkpoint, trazados, sectores, mediciones y estado de recepción
son las cinco tablas de `sql/ddl.sql`. Resultados, estados y checkpoint se
confirman atómicamente. Los conteos operacionales van a logs, no a otra tabla.

No se publican prefijos provisionales. Si una secuencia permanece abierta hasta
perder su raw o falta su geometría exacta, se detiene el productor sin resetear ni
saltar el checkpoint; H0011 continúa. No hay backfill, archivo GPS adicional ni
purga inicial de pasajes. No recargar GTFS con el worker activo.

Tests de dominio y PostGIS verifican particiones/reinicios, tardíos, ambigüedad
reconsiderada, barreras de scope, cierres por capturas reales, soporte/calidad,
rollback y concurrencia. La paridad batch se compara con la misma evidencia
aceptada y cerrada, no con una ventana histórica arbitraria censurada.

## Próximo paso

El esquema operacional está aplicado en la base local. El corte del 21/10/2026
verificó configuración activa y pasajes persistidos; ver
[estado operacional](h0012_estado_operacional.md).
Los parámetros experimentales y la cadencia están en
`internal/config/parameters.go`. El worker lo ejecuta siempre: inicializa su
configuración en el primer arranque sin backfill y recupera el checkpoint en los
siguientes. Falta medir costo, atraso e impacto sobre captura con GCBA disponible.
La [detección experimental](h0012_deteccion.md) lee los pasajes finales sin
cambiar este procesamiento. Calibración definitiva, consolidación territorial y
ranking siguen pendientes; esta simplificación no cambia políticas de medición.
