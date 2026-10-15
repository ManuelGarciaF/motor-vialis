# H0012 — Análisis experimental de sólo lectura

Plan: [plan_h0012_cuellos_botella.md](plan_h0012_cuellos_botella.md).
Spike anterior: [spike_h0012_resultados.md](spike_h0012_resultados.md).

## Decisiones confirmadas

- Matcher conservador: usar la secuencia GPS para resolver alternativas; no
  escoger la más cercana cuando persiste la ambigüedad.
- Herramienta de análisis de sólo lectura antes de worker/persistencia.
- Distancias **geodésicas** (PostGIS geography con esferoide), no UTM.
- Límites experimentales iniciales: 180 s / 100 m / 100 km/h.
- Considerar todas las proyecciones localmente mínimas dentro del radio; no
  aplicar un margen de distancia respecto de la alternativa más cercana.
- Admitir pasajes con y sin GPS interior, manteniendo metadatos de soporte.

## Implementación

`cmd/bottlenecks-analysis` ejecuta `bottlenecks.Analyze` con un repositorio en
`internal/database/postgres`. Consulta un recorrido/sentido por ID de catálogo y
una ventana GPS explícita `[from,to)`; presupuesto técnico de ejecución: 5 min.
No consulta al proveedor ni modifica captura, eventos, catálogos o raw.

El repositorio carga catálogo y observaciones en una transacción read-only,
repeatable-read. Requiere **PostGIS >=3.4**, por `ST_ClosestPoint(geography)`;
se verificó con 3.6.4. La progresiva es la suma de longitudes geodésicas de
aristas previas más la longitud geodésica del prefijo de la arista proyectada.
La longitud completa se mide con el mismo tipo geográfico. Los límites de
sectores se estabilizan a micrómetros, como en el dominio.

Para cada observación se buscan las aristas dentro del radio, se proyecta con
geography y se conservan los mínimos locales de distancia entre aristas
adyacentes. Proyecciones sobre el mismo vértice/progresiva se deduplican;
pasadas distintas por el mismo punto conservan progresivas distintas. No se
recuperan automáticamente geometrías históricas: otro hash/shape queda sin
correspondencia espacial y conserva su observación como barrera de continuidad.

El matcher temporal:

1. Ordena por unidad y timestamp, sin modificar al caller.
2. Usa alternativas espaciales únicas como anclas.
3. Filtra alternativas de observaciones ambiguas mediante anclas inmediatamente
   vecinas, de la misma identidad, dentro del hueco máximo y con avance no
   negativo que respete la velocidad máxima.
4. Propaga resoluciones hacia adelante/atrás hasta estabilizarse.
5. Conserva como ambiguas las alternativas no resueltas. Cero alternativas queda
   unmatched. Huecos, cambios de identidad o falta de anclas no justifican elegir.

**No es un optimizador completo de trayectorias.** Las anclas espaciales únicas
pueden tener ruido: si su continuidad es incompatible, el medidor corta el
pasaje. El método prioriza perder evidencia antes que inventar una pasada; no
permite afirmar que cada correspondencia aceptada sea correcta.

## Ejecución

```sh
# DATABASE_URL previamente cargada sin imprimir credenciales.
go run ./cmd/bottlenecks-analysis \
  -line-id 1015 \
  -from '2026-10-09T23:50:52.000001Z' \
  -to '2026-10-10T23:50:52.000001Z' \
  -segment-meters 500 > .local/h0012/analysis-1015-500.json
```

`line-id` es `id_recorrido`, no el número público. Flags experimentales:
`-max-gap=3m`, `-max-distance=100`, `-max-speed=100`; defaults para este análisis,
no parámetros de producción. `-database-url` puede sobreescribir el entorno;
no incluir credenciales en comandos/logs compartidos. En el contenedor, el host
puede ser `vialis-motor-db:5432`.

El reporte JSON contiene identidad/versionado, período y filtros efectivos
(`MaximumGapSeconds` en segundos), conteos de matching/rechazo y todos los
sectores: pasajes, soporte interior, mismo intervalo GPS, mediana de duración y
velocidad típica (= longitud / mediana de duración). **Sin pasajes: métricas
null**, no velocidad cero. No expone identificadores de unidades ni posiciones
GPS. Es un reporte CLI experimental, no un contrato HTTP definitivo.

## Primeras ejecuciones reales

Ventana UTC fija `[2026-10-09T23:50:52.000001Z,
2026-10-10T23:50:52.000001Z)`. Se seleccionaron tres recorridos con mayor número
de posiciones disponibles para verificar funcionamiento, **no una muestra
representativa ni un Top 10 de problemas**. El worker siguió operando.

| ID / línea / ramal / sentido | Raw | Espacialmente únicos | Resueltos temporalmente | Ambiguos | Sin match | Pasajes 500 m |
|---|---:|---:|---:|---:|---:|---:|
| 1015 / 622 / R3 / 0 | 1.405 | 787 | 0 | 27 | 591 | 661 |
| 1155 / 53 / C / 0 | 1.387 | 1.225 | 12 | 43 | 107 | 777 |
| 1719 / 194 / F / 0 | 1.237 | 996 | 0 | 7 | 234 | 1.363 |

Una posición puede soportar varios pasajes; los conteos no son tasas de éxito
ni cobertura del parque del AMBA. Los valores sin match incluyen falta de
alternativas espaciales o incompatibilidad temporal; aún no se desglosan sus
causas. En 622/R3 hay una pérdida importante de correspondencia que requiere
investigación antes de relajar filtros o confiar en su cobertura territorial.

Para 622/R3 se compararon tres longitudes con los mismos filtros:

| Longitud | Pasajes | Con GPS interior | Ambos cruces en un mismo intervalo GPS |
|---|---:|---:|---:|
| 300 m | 1.172 | 477 | 695 |
| 500 m | 661 | 412 | 249 |
| 800 m | 384 | 319 | 65 |

En esta muestra sin cruces exactos intermedios, los dos conteos de soporte
suman los pasajes; eso no es una identidad universal (hay cruces coincidentes
con observaciones en los límites). No comparar directamente contra el spike
anterior global: cambian población, matching y método de distancia.

## Pruebas y pendientes

Pasaron `go test ./...` (DB opt-in omitida), `go build ./...`, `go vet ./...` y
race del dominio. También `TestBottlenecks*` contra PostGIS real con schema
isolado y rollback: longitud/proyección geodésica, pasajes, ventana semiabierta,
recorrido inexistente, múltiples pasadas, versión vieja y puntos fuera del
radio. Tests unitarios cubren propagación de anclas, conservación de ambigüedad,
barreras temporales, identidades, duplicados y reporte sin datos. No se cambió
esquema ni datos persistentes del proyecto.

Pendiente: auditar causas de falta de match, inspeccionar mapas y trayectorias,
evaluar sensibilidad de sectores/franjas, separar terminales/regulación, extraer
geometría de sectores, persistencia incremental, referencias, recurrencia,
consolidación territorial, ranking y API. **Todavía no se detectan ni publican
cuellos de botella automáticamente.** No modificar filtros para mejorar conteos
sin una decisión explícita.
