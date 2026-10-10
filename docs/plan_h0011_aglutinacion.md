# H0011 — alcance y decisiones

## Estado

- Fases 0–4 implementadas: cliente GCBA, equivalencias, persistencia, worker,
  detector y consultas actuales/históricas.
- Migraciones aplicadas en la base del proyecto; el worker ya produjo eventos
  reales. Esto no certifica qué versión está ejecutando cada despliegue de la API.
- **Fase 5 descartada por el usuario:** no se implementan pasos por parada,
  headways, CV ni `GET /lines/{id}/regularity`. El spike no se continúa.
- Pendientes operacionales: contraste de encuentros con el servicio real,
  dimensionamiento sostenido de disco/WAL, cuota de polling y purga física de
  eventos a 30 días. El límite de consulta de 30 días no realiza esa purga.

La guía de ejecución, persistencia y contrato HTTP está en
[`h0011_aglutinaciones.md`](h0011_aglutinaciones.md); el contrato completo en
[`openapi.yaml`](openapi.yaml). No hay un documento por fase.

## Qué resuelve

Detectar pares de unidades cercanas del mismo scope operacional, confirmar su
continuidad con nuevas mediciones y conservar la evolución del encuentro.
No mide regularidad del servicio, frecuencia, pasajeros ni severidad.
Estar cerca puede deberse a una terminal, detención o ruido GPS; no demuestra
por sí solo una anomalía ni un headway corto.

Es independiente de `simulation.Service`: no modifica demanda, tiempo de viaje
ni recaudación de rutas propuestas.

```text
GCBA → cmd/realtime-worker → capturas/raw/eventos en PostgreSQL/PostGIS
                                      ↓
                                 cmd/api
                          /lines/{id}/bunching
                                 /bunching
```

## Decisiones del modelo

| Tema | Decisión |
|---|---|
| Proveedor | GCBA `vehiclePositionsSimple`, JSON, feed completo |
| Polling | Un minuto de espera después de cada ciclo; flag `-interval`; una instancia |
| Proximidad | Distancia geodésica directa <=100 m (`config.BunchingDistanceMeters`); H3 no interviene |
| Confirmación | Al menos dos observaciones; avanzan ambos timestamps GPS |
| Publicación en API | Duración GPS >=15 minutos y máximos de velocidad de ambas unidades >=1 m/s (`config.BunchingMinimumDuration`, `config.BunchingMinimumSpeedMetersPerSecond`); los encuentros no elegibles siguen almacenados |
| Identidad | GCBA ruta/sentido + GTFS ruta/sentido + versión geométrica |
| Agrupación | Pares independientes; A–B y B–C no implican A–C |
| Frescura | GPS de hasta cinco minutos; skew futuro máximo de un minuto |
| Cobertura parcial | Conserva captura/raw, sin abrir, actualizar ni cerrar encuentros |
| Raw | Siete fechas UTC; particiones completas, mañana precreada |
| Salida | Actual por recorrido, global actual/histórica y resumen de recurrencia semanal, sin colores |
| Recurrencia | Franjas locales fijas de una hora por ruta GTFS/sentido, con encuentros elegibles en >=2 fechas distintas dentro de las últimas 168 horas; cuenta cada franja que solapa la evidencia GPS, sin exigir los mismos vehículos o zona |
| Regularidad | Pasos/headways/CV descartados |

Los parámetros analíticos y límites defensivos están versionados en
`internal/config/parameters.go`. Entorno: conexión, dirección HTTP y credenciales;
no se cambia el radio mediante variables de entorno.

## Correspondencia GCBA–GTFS

Los IDs y nombres del tiempo real no corresponden de forma fiable al GTFS
estático disponible (snapshot de 2021 frente a posiciones de 2026). No vincular
por nombre, `direction` o coincidencia de `trip_id` durante una request.

El resultado administrado está en `vialis.equivalencias_recorridos_gcba`:

```text
gcba_route_id + gcba_direction → gtfs_route_id + gtfs_direction_id
```

Varias identidades GCBA pueden apuntar al mismo destino GTFS, pero **no se
mezclan al detectar pares**. Los IDs internos del catálogo se regeneran y no
son claves históricas.

La versión SQL contiene 814 equivalencias: 248 candidatos fuertes y 566 ambiguos
aceptados por decisión del usuario, tomando en estos últimos el mayor score.
No significa que las 814 tengan confianza alta. Los otros casos se ignoran y
se cuentan como no vinculados. La cobertura parcial del catálogo limita qué
líneas puede informar el endpoint.

El matching compara muestras deterministas de hasta 500 y 800 observaciones por
identidad: proximidad al shape, cobertura de su extensión y coherencia del avance.
La clasificación fuerte exige >=100 posiciones, cobertura >=90 % a 200 m y
>=70 % a 100 m, al menos 16/20 tramos observados, >=30 pares temporales útiles,
>=80 % de avance, margen de score >=0,10 y mismo ganador en ambas muestras.
Son criterios del proceso de selección, no filtros del detector en runtime.

Una auditoría independiente por `trip_id` obtuvo sólo 1,83 % de posiciones a
<=200 m del recorrido derivado y mediana de distancia de 14,7 km. Se descartó
esa alternativa: que un ID exista en ambos feeds no prueba que identifique el
mismo viaje. Tampoco garantiza correspondencia de paradas del endpoint completo.

Artefactos conservados:

- [`h0011_spatial_match_candidates.csv`](h0011_spatial_match_candidates.csv):
  selección espacial y fuente de `sql/recorridos/cargar_equivalencias_gcba.sql`.
- [`../scripts/README.md`](../scripts/README.md): comandos de reproducción.

No modificar equivalencias ni recargar GTFS desde una simulación o consulta HTTP.
Un GTFS contemporáneo y el decoder protobuf son mejoras pendientes, no requisitos
para mantener la detección espacial implementada.
