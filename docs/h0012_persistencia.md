# H0012 — Persistencia operacional de cuellos de botella

Complementa el [plan funcional](plan_h0012_cuellos_botella.md), el
[estado incremental](h0012_incremental.md) y la [operación del productor](h0012_productor.md).
Esta capa no implementa detección ni ranking; la lectura HTTP sobre sus pasajes
se describe en [detección experimental](h0012_deteccion.md).

## Datos que se conservan

Las definiciones están en `sql/ddl.sql`. `cmd/initdb` las crea junto con las
otras tablas finales, sin activar procesamiento ni incorporar histórico.

| Tabla | Finalidad operacional |
|---|---|
| `bottlenecks_modelos` | Configuración inmutable de medición, marca de activación y checkpoint de fuente |
| `bottlenecks_trazados` | Geometría y paradas versionadas que permiten interpretar mediciones después de una recarga GTFS |
| `bottlenecks_sectores` | Geometría, longitud y progresivas de los intervalos donde se mide cada pasaje |
| `bottlenecks_mediciones` | Pasajes completos/incompletos, motivo y soporte temporal/de calidad |
| `bottlenecks_estados_productor` | Estado mínimo por unidad para continuar la recepción tras reinicios, sin series GPS ni candidatos históricos |

Sólo existe un camino de procesamiento: `ProcessNext` → `ProduceCapture`.
Se eliminaron el cursor incremental resuelto y su persistencia, la tabla de
selección de modos y la tabla de diagnósticos por captura. Los conteos de calidad,
rechazos y avance se registran en logs después de confirmar trabajo; no hay una
segunda persistencia de telemetría. Los metadatos de calidad de cada pasaje sí
siguen en su medición.

`bottlenecks_modelos` no representa una plataforma de experimentos: conserva
cómo se midió cada resultado y desde qué captura continuar. Cambiar una política
no debe reinterpretar resultados existentes.

## Activación y recuperación

`Activate(PersistenceModel)` recibe nombre, versiones de algoritmos, longitud
nominal, política, zona Buenos Aires, cierre de cuatro capturas y retención raw
H0011. Esos días registran la política de la fuente, no cambian su retención.
El worker toma estos valores de `config.BottlenecksModel()`, definido en
`internal/config/parameters.go`. Son parámetros experimentales revisables en
código, no variables de entorno ni flags. La reanudación compara toda la política
persistida con config y rechaza diferencias sin sobrescribirla.

El worker inicializa automáticamente esta configuración única cuando no existe,
sin flags ni nombre elegido por el usuario. En reinicios sólo la recupera.
La primera activación excluye capturas existentes con `max(id_captura)`. Una
sección crítica corta usa el lock de ingesta y un lock SHARE de capturas para
incluir inserciones en vuelo. No se mantiene ese lock durante medición.
Repetir la activación con la misma configuración recupera su marca y checkpoint;
otra configuración con el mismo nombre falla con `ErrModelChanged`.

`LoadProducerStates` recupera configuración, checkpoint y estados bajo el mismo
lock de fila usado por el escritor. No activa ni resetea nada. Un reinicio no
reconstruye estados desde checkpoint cero.

## Trazados y sectores

Los snapshots conservan geometría completa, identidad GTFS/sentido/hash, longitud
y paradas ordenadas. No hay FK hacia IDs GTFS regenerables ni hacia raw/capturas
sujetos a purga. Una recarga del catálogo no borra evidencia estática.

La asociación de paradas conserva alternativas locales dentro del radio del
modelo; no fuerza una rama en lazos ni resuelve terminales/regulación. Si falta
la geometría exacta requerida por el raw, el productor se detiene sin sustituir
otro trazado. No recargar GTFS mientras corre el worker.

## Una transacción por captura

`postgres.BottlenecksStore` implementa `bottlenecks.ProducerRepository`.
`ProcessNext` es el único entry point de procesamiento y es dueño de su
transacción; los helpers no confirman por separado.

1. Bloquea configuración/checkpoint y valida retención.
2. Fija padre/particiones raw con ACCESS SHARE y vuelve a validar la referencia
   original después del lock, incluso al cruzar medianoche UTC.
3. Lee la siguiente captura y verifica conteos canónicos contra `filas_insertadas`.
4. Avanza recepción de todas las unidades; sólo las secuencias que cierran se
   rematchean y miden con su evidencia canónica completa.
5. Guarda mediciones finales, estados y checkpoint atómicamente.

Las capturas parciales aportan novedades, pero no suman ausencias. Errores del
proveedor no son capturas. Reinicios, reintentos y escritores concurrentes no
repiten una captura confirmada. Un fallo no entrega mediciones o estados parciales.

La identidad de medición usa configuración, trazado, unidad, scope, sector y
entrada UTC exacta. Un completo y un incompleto con la misma identidad no pueden
coexistir. JSONB conserva soporte con nanosegundos; `timestamptz` auxiliar tiene
precisión de microsegundos y no define por sí solo la identidad. Fecha/hora local
se calculan desde la entrada original, sin redondearla.

## Fuente y retención

Se controla la fecha UTC de activación/última captura, captura objetivo y raw OPEN
más antiguo. Si falta o expira contexto necesario, no se salta el checkpoint ni
se fuerza un cierre: se requiere una decisión administrativa explícita.
El pin raw permite INSERT normales, pero hace esperar la purga de particiones.
Conteos y fingerprint verifican la evidencia al releerla.

Se conserva sólo el descriptor OPEN, no un nuevo archivo GPS. Por eso una unidad
que actualice continuamente puede demorar resultados hasta agotar retención y
detener el productor. Tras purgar raw se puede recalibrar clasificación sobre
pasajes, pero no volver a medirlos con otro matching/segmentación.

## Base local y validación

La base local `vialis` usa este esquema reducido de cinco tablas. La actualización
fue puntual, sin script de migración permanente ni cambios a H0011. El corte del
21/10/2026 ya verificó configuración activa y mediciones persistidas; ver
[estado operacional](h0012_estado_operacional.md). No ejecutar todo `ddl.sql`
sobre una base poblada.

Los tests PostGIS crean y eliminan sus propios esquemas. Cubren DDL completo,
activación sin backfill, snapshots, soporte submicrosegundo, recuperación,
concurrencia, rollback de cierre y pérdida/purga de fuente. Comandos:

```sh
go test ./...
go build ./...
go vet ./...
TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/database/postgres \
  -run '^TestProducerStore|^TestBottlenecksStore|^TestBottlenecks' -count=1
```
