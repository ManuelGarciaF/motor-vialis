# Mapa del código

Guía de navegación y límites de responsabilidad. No reemplaza la
[especificación funcional de simulación](arquitectura_motor.md), el
[contrato HTTP](openapi.yaml) ni las decisiones de cada historia.

## Procesos y flujos

```text
cmd/api ──► internal/httpapi ──► internal/app / servicios de dominio
                                      │
                                      ▼
                              interfaces de repositorio
                                      │
                                      ▼
                           internal/database/postgres ──► PostgreSQL

cmd/simulation-test ──► internal/app ──► el mismo servicio de simulación

cmd/realtime-worker ──► internal/transport/gcba
                    └► internal/realtime ──► persistencia H0011 / aglutinaciones

cmd/bottlenecks-analysis ──► internal/bottlenecks ──► lector PostGIS (sólo lectura)

H0012: MeasureCapture ──► StreamTransition ──► PassageStore
       productor/lectura/matching incremental e integración al worker: pendientes

cmd/initdb ──► internal/database/bootstrap ──► scripts sql/ y herramientas ETL
```

El API atiende requests; no captura GPS ni ejecuta la ETL. H0011 es otro proceso.
H0012 tiene medición y almacenamiento probados, pero no un ciclo operativo que
los conecte. Crear tablas no inicia procesamiento ni activa modelos.

## Dónde buscar cada responsabilidad

| Área | Entradas / archivos | Responsabilidad |
|---|---|---|
| Composición | `internal/app`, `cmd/api`, `cmd/simulation-test` | Construir los mismos estimadores y repositorios, leer configuración |
| Transporte HTTP | `internal/httpapi` | Decodificar, validar formato y traducir errores/respuestas; no SQL ni estimadores propios |
| Simulación | `internal/simulation/service.go` y subpaquetes | Validar ruta → demanda → tiempo → recaudación; comparar variantes y contribuciones |
| Catálogo | `internal/lines` | Exportar recorridos almacenados y buscar similitud geométrica |
| Combinaciones | `internal/combinaciones` | Consultar rankings precalculados, sin inferir qué itinerario usó cada pasajero |
| Desvíos | `internal/simulation/detour`, `internal/traffic` | Analizar cortes, seleccionar/reconstruir variantes y usar tráfico en el ruteo |
| Captura H0011 | `internal/realtime`, `internal/transport/gcba`, `cmd/realtime-worker` | Captura, controles de calidad, almacenamiento y ciclo de aglutinaciones |
| Aglutinaciones | `internal/bunching` | Reglas de proximidad, continuidad y eventos |
| Medición H0012 | `internal/bottlenecks` | Layouts, matching conservador, pasajes, cobertura y estado incremental de medición |
| Acceso a datos | `internal/database/postgres` | Implementar interfaces de dominio, ejecutar SQL y controlar transacciones |
| Construcción de datos | `internal/database/bootstrap`, `sql/` | Crear/cargar el esquema en orden y recargar GTFS explícitamente |
| Configuración | `internal/config/config.go`, `parameters.go` | Entorno/secretos separados de constantes revisables del modelo |

Los paquetes de dominio no importan repositorios Postgres concretos. El SQL de
consulta vive en `internal/database/postgres`; las consultas complejas son
archivos `.sql` embebidos, con parámetros y adaptación de esquema en Go. Las
operaciones administrativas pequeñas pueden ser strings raw multilínea. Los
scripts de DDL/ETL viven en `sql/` y se embeben desde `sql/sql.go`.

## Persistencia H0012: recorrido de lectura

La interfaz está en
[`internal/bottlenecks/persistence.go`](../internal/bottlenecks/persistence.go).
La implementación se divide por responsabilidad, no por capas adicionales:

- [`bottlenecks_store.go`](../internal/database/postgres/bottlenecks_store.go):
  activación, recuperación de estado y controles comunes del repositorio.
- [`bottlenecks_store_layout.go`](../internal/database/postgres/bottlenecks_store_layout.go):
  snapshot de geometría, sectores y alternativas de asociación de paradas.
- [`bottlenecks_store_capture.go`](../internal/database/postgres/bottlenecks_store_capture.go):
  transacción por captura, validación de contexto, streams y checkpoint final.
- [`bottlenecks_store_measurement.go`](../internal/database/postgres/bottlenecks_store_measurement.go):
  representación e identidad de mediciones completas/incompletas.
- [`bottlenecks_store_validation.go`](../internal/database/postgres/bottlenecks_store_validation.go):
  consistencia del soporte y contadores contra metadatos reales de captura.
- [`bottlenecks_store_queries.go`](../internal/database/postgres/bottlenecks_store_queries.go):
  declaraciones `go:embed` de consultas de persistencia.

Los entry points públicos son dueños de la transacción; los helpers no hacen
commit. Un checkpoint sólo avanza junto con todas sus mediciones, diagnósticos
y estados. Los resultados no dependen de FKs a IDs GTFS regenerables ni al raw
que H0011 purga. El soporte y estado JSON conservan nanosegundos; `timestamptz`
auxiliar tiene precisión de microsegundos. La identidad no se construye usando
únicamente ese timestamp truncado.

Detalles y obligaciones del productor: [persistencia H0012](h0012_persistencia.md).
Medición sin confundir fin de lote con cierre: [estado incremental](h0012_incremental.md).
Políticas, calibración y alcance pendiente: [plan H0012](plan_h0012_cuellos_botella.md).
Estos documentos son las referencias del tema; este mapa no replica sus umbrales.

## Datos y pruebas

- Orden de instalación: `internal/database/bootstrap.steps`; operación en
  [README](../README.md), sin migraciones implícitas al iniciar el API/worker.
- Movilidad/H3/OD: [pipeline de viajes](../sql/viajes/README.md).
- GTFS, tiempos y conexiones: [pipeline de recorridos](../sql/recorridos/README.md).
- Grafo vial y giros: [pipeline de calles](../sql/calles/README.md).
- H0011: [operación y API](h0011_aglutinaciones.md).
- Unitarios: `*_test.go` junto al dominio; `go test ./...` no requiere DB.
- Integración: `TEST_DATABASE_URL` habilita pruebas PostgreSQL. Los tests nuevos
  de persistencia H0012 y binding de consultas crean/eliminan esquemas aislados;
  no asumir que toda la suite histórica tiene ese aislamiento.
- Validación de cambios: tests, `go build ./...`, `go vet ./...` y `gofmt -l .`.
  Race requiere CGO y un compilador C en el entorno.

Al modificar una regla, conservar desempates deterministas, unidades y contrato
transaccional, y agregar pruebas en la capa que define esa regla. Un refactor de
lectura no debe cambiar parámetros, corregir geometrías ni reinterpretar datos.
