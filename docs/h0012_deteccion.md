# H0012 — API de detección experimental

El usuario aprobó comenzar la API con la regla simple probada en el mapa:
**sector recurrente si tiene al menos dos pasajes completos con velocidad comercial
estrictamente menor a 6 km/h dentro del período solicitado**. Cuenta pasajes,
aunque sean de una misma unidad y fecha. No exige recurrencia entre días.

Esto habilita publicación experimental sin esperar la calibración del detector
territorial definitivo propuesto en el [plan](plan_h0012_cuellos_botella.md).
No demuestra causas viales ni reemplaza las validaciones operacionales pendientes.

## Contrato

`GET /bottlenecks?from=2026-10-21T18:00:00-03:00&to=2026-10-21T20:00:00-03:00`

- `from` y `to` obligatorios, RFC3339 con zona, con `from < to`.
- Intervalo semiabierto `[from,to)`, por entrada estimada, no por cierre/commit.
- `line` opcional: etiqueta exacta, por ejemplo `132`; no ID GTFS ni ID de catálogo.
- `bbox` opcional: `minLon,minLat,maxLon,maxLat` WGS84; intersección con geometría.
- Parámetros desconocidos, repetidos, fechas o bbox inválidos: 400.
- Sin límite de sectores, paginación, score ni Top 10. El timeout HTTP limita
  trabajo; un timeout falla la consulta completa, no entrega resultados truncados.

Devuelve `experimental: true`, período normalizado a UTC, política aplicada,
progreso de fuente y sectores elegibles. Cada sector incluye geometría, longitud
real, identidad de trazado/versionado, línea/ramal/sentido, indicador de extremo,
conteos completos/incompletos/lentos, porcentaje lento sobre completos y mediana
de velocidad de todos los completos. Sin IDs de unidades ni GPS individuales.
El orden es estable por recorrido, sentido, hash, layout y sector; no es ranking.
El contrato detallado está en [OpenAPI](openapi.yaml).

## Semántica y límites

- Mantiene detenciones por pasajeros. Sólo mide velocidad cuando hay pasaje
  completo: no convierte desapariciones o incompletos en circulación lenta.
- Primer y último sector siguen incluidos con `routeEnd: true`; revisar esperas
  de regulación. Excluir extremos fue una propuesta, no la política adoptada.
- 500 m en 5 min equivalen a 6 km/h, pero algunos sectores tienen distinta
  longitud real. Se usa velocidad real `<6`; exactamente 6 km/h no es lento.
- La unidad es sector de recorrido/versionado. Líneas coincidentes pueden
  devolver sectores distintos; no se consolidan ni mezclan mediciones.
- Sólo usa la configuración operacional seleccionada por config. No cambia
  política de medición, modelos persistidos, checkpoint, matching ni retención.
- Sólo mediciones finales. OPEN no aporta resultados todavía; un período pasado
  puede recibir pasajes cuando cierre una secuencia posterior.
- `source.initialized: false` y lista vacía significa que aún no hay configuración
  del productor. Configuración existente sin sectores elegibles también devuelve
  lista vacía: **ninguna lista vacía acredita ausencia de lentitud o cobertura**.
- `source.checkpoint` y `lastCaptureReceivedAt` informan progreso, no certifican
  cobertura ni salud actual del worker. Se consultan en el mismo snapshot que
  los sectores. No exponen una garantía de completitud del período.

## Implementación y validación

- Dominio y validación: `internal/bottlenecks/detection.go`.
- Regla revisable en código: `config.BottlenecksDetectionPolicy()`.
- Repositorio: `internal/database/postgres/bottlenecks_detection.go`, con SQL
  embebido adyacente. Una consulta SELECT parametrizada; no inicia procesamiento.
- HTTP: `internal/httpapi/bottlenecks.go`; composición en `internal/app` y `cmd/api`.
- Tests de dominio/HTTP y prueba PostGIS con tablas TEMP de sesión y rollback:

```sh
go test ./...
TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/database/postgres \
  -run '^TestBottlenecksDetectionRepository$' -count=1
```

Pendiente para una versión no experimental: calibración de umbrales y evidencia,
comparabilidad por franjas/días, tratamiento más fino de cabeceras, consolidación
territorial, ranking y evaluación longitudinal. No bloquean este contrato inicial.
