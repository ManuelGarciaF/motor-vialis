# H0012 — Detección de cuellos de botella

Identifica sectores de recorridos donde se observan pasajes recurrentemente lentos.
El worker mide y conserva pasajes; la API aplica el criterio de detección sobre
esas mediciones, sin capturar GPS ni ejecutar matching durante un request.

## Criterio actual

Un sector se devuelve como cuello de botella cuando tiene **al menos dos pasajes
completos a menos de 6 km/h en el período solicitado**. Se cuentan pasajes, aunque
sean de una misma unidad o fecha; no se exige repetición entre días.

- La velocidad es comercial: distancia del sector / tiempo de atravesamiento.
  Incluye detenciones por pasajeros, no promedia velocidades instantáneas.
- Si el vehículo no completa el sector, no se inventa velocidad ni se considera
  lento: queda incompleto con su motivo.
- Los sectores inicial/final siguen incluidos, señalados por `routeEnd`; pueden
  contener esperas de regulación de cabecera. No se excluyen automáticamente.
- La regla es inicial y no demuestra una causa vial. Una lista vacía tampoco
  demuestra circulación fluida, cobertura completa ni ausencia de problemas.

Umbrales en `internal/config/parameters.go`:

```go
BottlenecksSlowSpeedKPH        = 6.0
BottlenecksMinimumSlowPassages = 2
```

El corte es estricto para velocidad (`<`) e inclusivo para cantidad (`>=`).
Cambiar estos valores requiere recompilar/reiniciar la API, no volver a medir
pasajes ni cambiar el estado del worker.

## Cómo se miden los pasajes

1. Reutiliza el raw canónico y las equivalencias GCBA→GTFS de H0011, con ruta,
   sentido y versión geométrica explícitos. Los IDs de ambos proveedores son
   namespaces distintos. No corrige mappings ni reasigna por cercanía a otro shape.
2. Divide la geometría real por distancia longitudinal geodésica en sectores
   nominales de **500 m**, por recorrido/sentido/versión. El remanente final menor
   a media longitud nominal se une al anterior; la longitud real puede variar.
3. Proyecta con PostGIS `geography` y conserva todas las alternativas locales
   dentro del radio. El matcher usa anclas espaciales únicas y continuidad temporal;
   si persiste la ambigüedad, no elige simplemente el candidato más cercano.
4. Interpola los cruces de entrada/salida sólo entre observaciones compatibles.
   Se admiten pasajes sin GPS interior, incluso ambos cruces en un mismo intervalo,
   conservando soporte temporal, conteo interior y máximo hueco de soporte.
5. Corta continuidad ante huecos, retrocesos, saltos implausibles, matching no
   resuelto o cambio de identidad/versión. No extrapola entradas o salidas faltantes.

Política de medición actual: máximo **180 s** entre GPS, **100 m** al shape y
**100 km/h** longitudinales. No confundir esos límites con el umbral lento de 6 km/h.
Para 500 m, 5 min equivalen a 6 km/h, pero la detección usa velocidad sobre longitud
real. El análisis horario atribuye cada pasaje una sola vez por entrada estimada,
en `America/Argentina/Buenos_Aires`, aunque cruce hora o fecha.

## Recepción, cierre y reinicios

El productor corre siempre dentro de `realtime-worker`, con ciclo y pool propios,
sin solapamientos ni cálculo dentro de la transacción de captura H0011.
El único camino operacional es `ProcessNext` → `ProduceCapture`.

- Primer arranque: inicializa configuración/checkpoint excluyendo capturas ya
  almacenadas, **sin backfill**. Reinicios recuperan la marca y el estado original.
- Mientras una secuencia está abierta (OPEN), guarda sólo un descriptor con
  referencias al raw/layouts/scopes, límites GPS, contador y fingerprint; no otro
  archivo de coordenadas, observaciones o candidatos históricos.
- Cierra tras **cuatro capturas correctas no parciales consecutivas sin timestamp
  GPS más nuevo**. Procesa novedades antes de contar ausencias. Un timestamp nuevo
  reinicia el contador aunque la coordenada no cambie o no tenga matching.
  Parciales aportan novedades pero no suman ausencias; errores, ticks y reintentos
  tampoco cuentan. Cuatro capturas no equivalen a cuatro minutos ni amplían 180 s.
- Se eligieron cuatro tras auditar llegadas tardías: se observaron pares compatibles
  después de tres ausencias, pero no después de cuatro en esa muestra. No garantiza
  que nunca llegue evidencia útil después del cierre.
- Al cerrar relee el raw aceptado, verifica conteo/fingerprint, ordena por tiempo
  GPS y rematchea la secuencia completa. Un cambio A→B→A es una barrera, no se une.
  GPS tardío puede modificar OPEN; una secuencia cerrada **no se reabre**. Un GPS
  posterior puede iniciar otra secuencia, sin corregir el resultado anterior.
- Sólo confirma resultados finales, sin prefijos provisionales. Entradas sin salida
  quedan incompletas, sin calcular duración hasta la hora administrativa de cierre.

## Persistencia y retención

Cinco tablas, definidas en `sql/ddl.sql`:

| Tabla `vialis.bottlenecks_*` | Conserva |
|---|---|
| `modelos` | Política de medición inmutable, activación y checkpoint |
| `trazados` | Geometría completa, identidad/versionado y paradas |
| `sectores` | Geometría, longitud y progresivas |
| `mediciones` | Pasajes completos/incompletos y soporte de calidad |
| `estados_productor` | Recepción por unidad y descriptor OPEN |

Una transacción por captura bloquea configuración/checkpoint, valida retención,
fija particiones raw con ACCESS SHARE y verifica filas canónicas. Mediciones,
estados y checkpoint se confirman **atómicamente**, sin duplicar al reintentar o
reiniciar. Los locks raw permiten INSERT normales, pero pueden demorar la purga.
La proyección reutiliza por captura una tabla TEMP indexada de aristas originales;
no modifica geometría ni matching. Los diagnósticos de avance/rechazo van a logs.

Los snapshots no dependen de IDs GTFS regenerables ni de FKs a raw sujeto a purga.
La identidad de medición conserva entrada UTC exacta; el JSON mantiene nanosegundos,
no se usa sólo el `timestamptz` auxiliar de precisión microsegundo.

H0011 conserva ahora raw y capturas sin purga automática mediante
`RealtimePreserveHistory`; no se crea otro archivo de GPS. La ventana de admisión
y el límite de antigüedad de fuente persistido en H0012 siguen siendo **siete
fechas UTC**: conservar archivos no habilita recuperación/reprocesamiento arbitrario
de checkpoints viejos ni cambia el modelo existente.
Pasajes y metadatos tampoco tienen purga inicial. Tras una purga administrada de
raw puede recalibrarse la
clasificación, pero no rehacer segmentación/matching de esas mediciones.
Una unidad continuamente activa puede mantener OPEN hasta agotar retención:
si falta/expira raw necesario o falta la geometría exacta, **se detiene H0012 con
aviso y H0011 continúa**, sin truncar, forzar cierres, resetear o saltar checkpoint.
Cambiar la política de medición persistida requiere administración explícita;
no se sobrescribe al reiniciar.

## API

```text
GET /bottlenecks?from=2026-10-21T18:00:00-03:00&to=2026-10-21T20:00:00-03:00
```

- `from`/`to` obligatorios, RFC3339 con zona, `from < to`. Con `DEMO_MODE=true` en `cmd/api` (o `-demo`),
  omitir ambos usa las 24 horas anteriores al reloj fijo de la última captura;
  una sola fecha sigue siendo inválida. Ver [operación de demo](../README.md#demo-con-capturas-existentes).
  Ventana **[from,to)**
  por entrada estimada, no por cierre ni commit.
- `line` opcional: etiqueta exacta, por ejemplo `132`, no ID de catálogo/GTFS.
- `bbox` opcional: `minLon,minLat,maxLon,maxLat` WGS84; intersección con sector.
- Respuesta: **`from`, `to`, `sectors`**. Cada sector incluye geometría, longitud,
  identidad de trazado/versión, línea/ramal/sentido, extremo, conteos completos,
  incompletos y lentos, porcentaje lento sobre completos y mediana de todos los
  completos. No exporta IDs de unidades ni GPS individuales.
- Sin límite de sectores, paginación ni ranking. Orden estable por recorrido,
  sentido, hash, layout y sector. Un timeout falla la consulta, no entrega truncados.
- Parámetros desconocidos/repetidos o valores inválidos: 400; lector no configurado:
  503; timeout: 504. Fuente sin activar o sin elegibles: 200 con lista vacía.

Sólo usa mediciones finales: OPEN puede aportar pasajes a períodos pasados cuando
cierre. Sectores de líneas coincidentes siguen separados; no son lugares
territoriales únicos. Contrato de campos y errores: [OpenAPI](openapi.yaml).

## Operación y código

```sh
# DATABASE_URL y credenciales GCBA exportadas; no se carga .env automáticamente.
go run ./cmd/realtime-worker
# Snapshot local del mapa, con Node >=20 y psql en PATH:
node scripts/worker-map.mjs --serve
```

No ejecutar un segundo poller permanente. `cmd/initdb` crea el esquema en bases
nuevas; el worker **no crea ni migra tablas**. No ejecutar todo el DDL sobre una
base poblada. Detener el worker antes de recargar GTFS o administrar el esquema.

Cadencia/recursos actuales en config: espera de 2 min, pool de dos conexiones,
turnos de hasta 32 capturas o 5 min. No aíslan CPU/IO/locks de H0011. Inspeccionar
`application_name=vialis-bottlenecks`, `bottlenecks_captures_committed` (incluye
`duration_ms`) y `bottlenecks_processing_stopped`. Fallos transitorios se reintentan;
errores de esquema/configuración al arrancar impiden iniciar el worker.

- Dominio, matcher, medición, recepción y detección: `internal/bottlenecks`.
- SQL/repositorios: `internal/database/postgres/bottlenecks_*` y consultas adyacentes.
- HTTP/composición: `internal/httpapi/bottlenecks.go`, `internal/app`, `cmd/api`.
- Análisis y auditorías de sólo lectura: `cmd/bottlenecks-analysis` y
  [herramientas administrativas](../scripts/README.md).

Validación: `go test ./...`, `go build ./...`, `go vet ./...`, `gofmt -l .`.
Los tests PostGIS requieren `TEST_DATABASE_URL`: usar una base de pruebas; no
asumir aislamiento de toda la suite contra el worker activo. Los tests cubren
matching, soporte, tardíos, reinicios, paridad batch, rollback, concurrencia,
retención y cortes de detección. No hace falta volver a capturar para probar HTTP.

## Límites y trabajo pendiente

Calibrar umbrales y sensibilidad, verificar cadencia/costo/crecimiento sostenidos,
revisar lazos/cabeceras y sesgos de evidencia. La cercanía a otro shape no identifica
servicio/sentido ni justifica corregir equivalencias o ampliar el radio.
La cobertura se limita a unidades observadas y vinculadas, no a toda la oferta AMBA;
GPS o incompletos no son un denominador de viajes reales perdidos.
Consolidación territorial entre líneas, días/franjas comparables y Top 10 siguen
pendientes. No inferir pasajeros afectados ni causas demostradas desde estos datos.
