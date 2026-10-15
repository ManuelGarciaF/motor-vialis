# H0012 — Cobertura de pasajes válidos por sector y hora

Plan: [plan_h0012_cuellos_botella.md](plan_h0012_cuellos_botella.md).
Población espacial: [h0012_auditoria_global.md](h0012_auditoria_global.md).

## Resumen

**Hay una base aprovechable para continuar:** se reconstruyeron **271.606
pasajes completos** y el **80,31% de los sectores de los recorridos observados**
tiene al menos uno. La evidencia por hora es desigual: la mediana de las celdas
con pasajes es **2 pasajes / 2 unidades**. No todos los sectores tienen evidencia
suficiente para publicar lentitud recurrente.

No se definieron mínimos de publicación, recurrencia ni ranking. Tener pasajes
válidos no demuestra lentitud, congestión ni causa vial.

## Decisiones y población

Confirmado por el usuario:

- Franjas fijas de **una hora local**, `America/Argentina/Buenos_Aires`.
- Cada pasaje completo se asigna una sola vez a la fecha/hora de su **entrada
  estimada**; no se divide el tiempo ni se duplica si cruza una franja/medianoche.
- Mantener sectores nominales **500 m**, geodesia y filtros **180 s / 100 m /
  100 km/h**. Se admiten pasajes con y sin GPS interior, como ya se aprobó.
- Ampliar sólo el presupuesto técnico global de la herramienta a **15 minutos**,
  conservando máximo **5 minutos por target** y para la ejecución individual.

La corrida completa terminó en **8 min 23,6 s**, con manifiesto y marcador
`complete` validados. La corrida anterior de 5 min terminó tras 329/563 targets;
su archivo parcial se archivó y **no se consolidó como resultado global**.
Los resultados de esos 329 targets son idénticos en ambas corridas: el cambio
fue sólo de tiempo disponible, no de medición.

Ventana GPS UTC fija:
`[2026-10-08T17:01:10Z,2026-10-11T00:46:52.000001Z)`.
Se analizaron **515.592 posiciones / 563 recorridos-ramales-sentidos**, usando
explícitamente las equivalencias activas de H0011 (`equivalencias_recorridos_gcba`).
Los IDs API/GCBA nunca se comparan como si fueran IDs GTFS.

Cada target se lee en un snapshot read-only/repeatable-read. El manifiesto
conserva versión de geometría y número esperado de observaciones; un cambio
entre selección y lectura aborta el lote, sin marcador final. No es una única
transacción larga para todos los targets ni un archivo que pueda considerarse
completo por tener muchos resultados parciales.

## Cobertura longitudinal

Los sectores son específicos de cada recorrido y versión. **No son lugares
territoriales únicos:** varias líneas pueden representar el mismo corredor.
Tampoco representan todo el catálogo o toda la red vial del AMBA.

| Situación | Sectores |
|---|---:|
| Total en geometrías de los targets observados | **31.306** |
| Con al menos un pasaje completo válido | **25.143 (80,31%)** |
| Con pasajes en ambas fechas observadas | **19.890 (63,53%)** |
| Con GPS resuelto o entrada incompleta, pero ningún pasaje completo | **2.304** |
| Sin evidencia de GPS resuelto ni entrada/pasaje reconstruido | **3.859** |

Ocho de los 563 targets no produjeron pasajes completos en esta ventana. Ausencia
no significa circulación fluida ni falta de servicio: puede faltar evidencia.
La mediana por sector, incluyendo ceros, es 6 pasajes; percentil 90: 21.

Se resolvieron progresivas para **437.336 observaciones**: 435.057 espacialmente
únicas y 2.279 resueltas mediante anclas temporales. Quedaron 8.690 ambiguas y
69.566 sin correspondencia admitida. No se elige la pasada más cercana para
mejorar artificialmente cobertura.

### Soporte de los pasajes completos

| Soporte temporal | Pasajes | Proporción |
|---|---:|---:|
| Al menos un GPS estrictamente interior | 189.017 | 69,59% |
| Ambos cruces interpolados dentro del mismo intervalo GPS | 82.140 | 30,24% |
| Sin GPS interior, con observaciones en límites | 449 | 0,17% |

Las tres categorías son disjuntas y suman 271.606. Ninguna fue excluida por
carecer de GPS interior. El tiempo comercial incluye las detenciones observadas;
no se reemplaza por velocidad instantánea del proveedor.

## Densidad temporal

Se emitieron **130.601 celdas sector/fecha/hora con alguna evidencia** y
**107.684 con pasajes completos**. No se generan como si hubiera servicio todas
las combinaciones posibles sector/calendario; no hay un denominador de oferta
programada o de horas de servicio completas.

Entre las celdas con pasajes:

| Estadístico | Pasajes | Unidades distintas |
|---|---:|---:|
| Percentil 10 | 1 | 1 |
| Mediana | **2** | **2** |
| Percentil 90 | 5 | 5 |
| Máximo | 29 | 29 |

Los GPS se cuentan en su hora observada, mientras que el pasaje se atribuye por
entrada: una celda puede tener pasajes válidos sin GPS propios, o GPS sin un
pasaje completo asignado. Estos conteos no deben confundirse.

Hay **41.124 pares sector/hora** con pasajes en las dos fechas observadas.
Como descripción del volumen —**no mínimos aprobados**—:

| Volumen `n` | Celdas con ≥n pasajes | Celdas con ≥n unidades | Pares sector/hora con ambas fechas, cada una con ≥n unidades |
|---|---:|---:|---:|
| 1 | 107.684 | 107.684 | 41.124 |
| 2 | 64.370 | 64.363 | 21.438 |
| 3 | 37.601 | 37.597 | 10.631 |
| 5 | 13.816 | 13.809 | 3.050 |
| 10 | 1.614 | 1.614 | 119 |

No se suman unidades distintas entre fechas o targets para afirmar un total
único: pueden repetirse. Estos pares tampoco son cuellos de botella detectados;
aún no comparan velocidad comercial con una referencia.

## Fechas y comparabilidad

| Fecha local | Tipo de día | Pasajes completos |
|---|---|---:|
| 2026-10-08 | Jueves | 159.328 |
| 2026-10-10 | Sábado | 112.278 |

La ventana tiene sólo **dos fechas locales, ambas parciales**, y ningún GPS del
09/10 local. Además, son un día hábil y un sábado: compartir hora y lugar no
prueba una recurrencia homogénea entre días comparables. No se mezcló esto en
una política de clasificación; el análisis describe disponibilidad, no demanda
ni operación normal de cada tipo de día.

## Mediciones iniciadas que no pudieron completarse

Se registran únicamente entradas con soporte GPS, sin extrapolar entradas
anteriores/desconocidas. Hay **52.331 mediciones incompletas**:

| Motivo | Mediciones |
|---|---:|
| Hueco GPS >180 s | 28.941 |
| Matching no resuelto (sin candidato o ambiguo) | 15.330 |
| Fin de observaciones de la unidad/lote, sin salida | 6.176 |
| Retroceso longitudinal | 1.559 |
| Cambio de identidad del proveedor dentro del mismo destino | 229 |
| Velocidad longitudinal implausible | 96 |

Los huecos pueden incluir interrupciones de captura, pausas/fin de servicio o
el salto entre fechas; no se interpretan como detenciones de tráfico. El fin de
lote es **censura**, no evidencia de fallo GPS ni de servicio. Un GPS inválido de
la siguiente unidad no se atribuye como causa del cierre de la anterior.

La identidad contable es `entradas soportadas = completos + incompletos`:
**323.937 = 271.606 + 52.331**. **No es la tasa real de viajes perdidos o de éxito**:
las entradas nunca observadas no están en el denominador y los sectores son
unidades de medición, no viajes completos de pasajeros.

## Ejemplos de la ventana completa

| Recorrido | Sectores con pasajes / total | Pasajes | Sectores con ambas fechas |
|---|---:|---:|---:|
| 622/R3/0 | 64 / 66 | 2.207 | 64 |
| 53/C/0 | 106 / 108 | 2.754 | 106 |
| 194/F/0 | 94 / 94 | 6.089 | 94 |

Incluso un target con pérdidas espaciales puede conservar mediciones útiles.
Esto no acredita suficiencia en cada hora ni exactitud de la identidad operativa.

## Reproducción y validación

```sh
# DATABASE_URL previamente cargada, sin imprimir credenciales.
go run ./cmd/bottlenecks-analysis -all \
  -from '2026-10-08T17:01:10Z' -to '2026-10-11T00:46:52.000001Z' \
  > .local/h0012/coverage-retained.ndjson
python3 scripts/summarize-h0012-coverage.py .local/h0012/coverage-retained.ndjson \
  > .local/h0012/coverage-retained-summary.json
python3 scripts/test-h0012-coverage-summary.py
```

Archivos locales sin versionar; el NDJSON completo ocupa unos 52,8 MB. El
consolidador rechaza lotes sin marcador final, IDs duplicados, cambios de
manifiesto/configuración, sumas inconsistentes, categorías de soporte incoherentes
y evidencia de unidades inválida. No consume ni publica IDs de unidades o GPS.

Pasaron `go test ./...` (integraciones opt-in omitidas), `TestBottlenecks*` contra
PostGIS con schema aislado/rollback, race del dominio/CLI, build, vet y formato de
fuentes. Tests del dominio cubren atribución única en medianoche, fechas y unidades
únicas, pasajes sin GPS interior, censura y ruptura de continuidad. Los diez tests
Python del consolidador pasaron sin ResourceWarnings. Se validaron todas las sumas
del lote real y la paridad de los 329 targets de la ejecución anterior.

## Conclusión y pendientes

**La medición es viable para una parte importante de la cobertura observada**,
pero no habilita todavía un mapa exhaustivo ni un Top 10 fiable de congestión.
Por hora muchos sectores tienen sólo una o dos unidades y las fechas no son
homogéneas. Hace falta más exposición temporal y acordar evidencia mínima y tipos
de día antes de clasificar recurrencia.

También quedan referencia espacial de lentitud, tratamiento de terminales y
regulación, inspección de trayectorias/sectores críticos, consolidación territorial,
persistencia incremental y API. No se modificaron mappings, radio, máximo hueco,
política de interpolación ni filtros para mejorar estos números.
