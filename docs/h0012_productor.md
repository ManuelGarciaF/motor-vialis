# H0012 — Productor y ciclo del worker

Estado: productor implementado y validado con datos sintéticos/PostGIS aislado;
sin modelo real activado ni calibración de detección. La operación real depende
de que GCBA vuelva a entregar capturas. Ver [plan funcional](plan_h0012_cuellos_botella.md),
[medición incremental](h0012_incremental.md) y [persistencia](h0012_persistencia.md).

## Qué se confirma y cuándo

El productor distingue recepción de GPS y medición final. Mientras una secuencia
está abierta, guarda sólo un descriptor operacional: unidad, capturas de primera
y última recepción, límites GPS, scopes/layouts, contador y fingerprint de la
evidencia canónica. No guarda listas GPS, coordenadas ni candidatos históricos.

Un timestamp nuevo reinicia ausencias aunque la unidad siga quieta o no tenga
matching. Las capturas parciales aportan actualizaciones pero no suman ausencia;
los errores del proveedor no generan capturas procesables. Se cierra tras cuatro
capturas correctas no parciales sin timestamp más nuevo, procesando primero sus
novedades. Los ticks del scheduler, reintentos e inactividad del procesador no
son capturas ni ausencias.

Al cerrar se relee el raw original aceptado, se verifica membresía por recepción,
conteo/fingerprint, y se ordena por tiempo GPS. El matcher se ejecuta sobre las
alternativas originales, no sobre candidatos ya podados. La medición final se
confirma una sola vez. Una entrada sin salida queda incompleta; el cierre no
inventa velocidad ni duración hasta la hora del cierre.

Los cambios de identidad proveedor, sentido o versión son barreras en ese orden
GPS: A→B→A no se une. Sus entradas pendientes terminan como `scope_changed`;
el remanente de la última secuencia usa `missing_updates`. La separación se
resuelve en el replay final, no se fija irreversiblemente por orden de llegada.
GPS tardío dentro de una secuencia abierta puede cambiar la reconstrucción;
evidencia anterior/al watermark final no reabre una secuencia cerrada.

**Latencia:** no se publica un prefijo provisional. Una unidad que actualice
continuamente puede demorar sus resultados indefinidamente. Si su evidencia
necesaria sale de retención, H0012 se detiene con aviso, sin truncarla ni forzar
un cierre adicional. Esta limitación es deliberada; no hay plazo de tardanza
inventado ni archivo adicional de GPS.

## Lectura y transacción

`BottlenecksStore.ProcessNext` procesa una captura real por transacción:

1. Recupera/bloquea el modelo y verifica modo/checkpoint y retención.
2. Fija el padre raw y sus particiones con `ACCESS SHARE`; vuelve a validar la
   referencia original después de obtener el lock, cubriendo cruces de fecha UTC.
3. Verifica el número de filas canónicas nuevas contra `filas_insertadas`.
4. Proyecta contra snapshots de geometría exactos, siguiendo la identidad guardada
   en raw, sin reinterpretar datos con equivalencias actuales.
5. Avanza todos los estados de unidades, incluidas ausencias. Hace replay sólo
   para las que cierran y verifica todo su contexto original.
6. Confirma estado de recepción, mediciones finales, diagnósticos y checkpoint
   atómicamente. Un fallo no entrega resultados parciales como confirmados.

El pin raw es compatible con INSERT normales, pero demora la purga de particiones
hasta terminar la transacción. No usa el advisory lock de ingesta. Los cambios de
particiones y snapshots del catálogo también comparten recursos PostgreSQL: la
separación de goroutines/pools no garantiza aislamiento de CPU, IO o DDL. Medir
el costo y el impacto sobre captura antes de dar por calibrada la cadencia.

`LoadProducerStates` recupera el estado confirmado sin activar modelos. Los modos
productor y cursor resuelto legado no se pueden mezclar sobre el mismo modelo.
Un modelo usado por `CommitCapture` requiere otro nombre para el productor;
no se convierte ni se resetea su checkpoint automáticamente.

No se soporta recargar GTFS durante la ejecución del worker: detenerlo antes de
actualizar el catálogo. Los snapshots ya materializados sobreviven a recargas;
si falta la versión exacta necesaria, se detiene H0012, sin sustituir otro shape.

## Migraciones explícitas

Instalaciones nuevas ejecutan ambos scripts desde `cmd/initdb`, sin activar
modelos. Para una base existente, ejecutar en orden con el worker detenido:

```powershell
psql -h localhost -p 5433 -U postgres -d vialis -X -v ON_ERROR_STOP=1 -f sql/bottlenecks/migrar_persistencia.sql
psql -h localhost -p 5433 -U postgres -d vialis -X -v ON_ERROR_STOP=1 -f sql/bottlenecks/migrar_productor.sql
```

La segunda migración incorpora `h0012_productores` y `h0012_estados_productor`.
En la base local de esta entrega sólo se aplicó la primera; **la segunda sigue
pendiente**. Ninguna se ejecuta implícitamente desde el worker. Crear tablas no
activa modelos ni incorpora histórico.

## Ejecución opt-in

Sin flags H0012 el proceso conserva exclusivamente la captura/aglutinaciones
H0011 y no consulta su esquema/modelo. Variables necesarias en ambos casos:
`DATABASE_URL`, `GCBA_TRANSPORT_CLIENT_ID`, `GCBA_TRANSPORT_CLIENT_SECRET`.
No se carga `.env` automáticamente; exportarlas sin compartir credenciales.

### Primera activación experimental

Requiere nombre, intervalo y los cuatro parámetros de medición explícitos.
**Los siguientes valores son ejemplos experimentales, no defaults aprobados de
producción ni una frecuencia validada:**

```powershell
$env:DATABASE_URL = "postgresql://postgres:postgres@localhost:5433/vialis"
go run ./cmd/realtime-worker `
  -h0012-model=experimento-v1 -h0012-interval=2m -h0012-activate `
  -h0012-segment-meters=500 -h0012-max-gap=180s `
  -h0012-max-distance-meters=100 -h0012-max-speed-kph=100
```

La activación y compatibilidad se verifican antes de iniciar ingesta. La marca
inicial excluye capturas existentes; no hay backfill. Reusar el nombre con otros
parámetros falla. El modelo registra versiones de algoritmos, zona, cierre y
retención de la fuente; no modifica H0011 ni sus filtros.

### Reanudación

```powershell
go run ./cmd/realtime-worker -h0012-model=experimento-v1 -h0012-interval=2m
```

No usar `-h0012-activate` ni volver a pasar parámetros de medición al reanudar.
Se recuperan configuración y checkpoint originales; si no existe el modelo, el
arranque falla explícitamente. El intervalo positivo se elige en cada arranque,
porque es cadencia operacional, no política de medición. Ejecutar una instancia.

### Recursos, errores y apagado

- Pool H0012 separado con máximo dos conexiones; no consume las del pool de ingesta.
- Un solo ciclo H0012, máximo 32 capturas o cinco minutos por turno, seguido del
  intervalo indicado. Son límites de trabajo, no cierres de secuencia ni nuevos
  límites de hueco GPS. Las capturas ya confirmadas sobreviven al fin del turno.
- Fallos transitorios/timeout: diagnóstico seguro y reintento en el siguiente ciclo.
- Pérdida/expiración de fuente, geometría exacta ausente o modelo incompatible:
  `h0012_processing_stopped`, sin resets ni salto de checkpoint; **H0011 continúa**.
- Errores de esquema/modelo al arrancar impiden iniciar el proceso habilitado.
- Cancelación o fin de ingesta cancela y espera H0012 antes de cerrar pools.
- Logs por clasificación y conteos, sin payloads GPS, identificadores de unidades,
  URLs autenticadas ni textos arbitrarios de drivers.

## Validación y pendientes

```powershell
go test ./...
go build ./...
go vet ./...
$env:TEST_DATABASE_URL = "postgresql://postgres:postgres@localhost:5433/vialis"
go test ./internal/database/postgres -run '^TestProducerStore|^TestPassageStore|^TestBottlenecks' -count=1
```

Tests sintéticos cubren todas las particiones de una trayectoria, reinicios JSON,
tardíos, ambigüedad reconsiderada, A→B→A y cierres por capturas reales. PostGIS
cubre replay/rollback/concurrencia, geometría exacta, modos, fuente perdida y una
purga intercalada en medianoche, además de compatibilidad de inserts durante el
pin. Tests de loop/CLI usan fakes y no llaman al proveedor real.

La revisión independiente del dominio, ciclo y corrección de purga no encontró
hallazgos pendientes. Falta calibración de costo/cadencia con capturas reales y
prueba operacional cuando GCBA funcione. Race depende de
CGO/compilador C y no se ejecutó en este entorno. Esto no implementa todavía
criterios de lentitud/recurrencia, consolidación territorial, ranking ni API H0012.
