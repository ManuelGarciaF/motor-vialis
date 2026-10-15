# H0012 — Auditoría global usando las equivalencias de H0011

Continuación de [h0012_auditoria_matching.md](h0012_auditoria_matching.md).
Solicitado: extender el análisis antes de decidir, usando la tabla administrada
que resuelve identidades GCBA y GTFS para aglutinaciones.

## Identidades y población

Se usa **`vialis.equivalencias_recorridos_gcba` activa**, no igualdad de IDs:

```text
raw.gcba_route_id + raw.gcba_direction
  → equivalencias.gcba_route_id + equivalencias.gcba_direction
  → equivalencias.gtfs_route_id + equivalencias.gtfs_direction_id
  → recorridos.gtfs_route_id + recorridos.direction_id
```

La ingesta H0011 ya aplica exactamente este cruce en
`internal/database/postgres/insert_realtime_positions.sql` al guardar el destino
GTFS en raw. La auditoría verifica de nuevo el mapping activo y compara destino,
sentido, shape y hash guardados. Si hay mapping ausente, destino inexistente o
cambio histórico de mapping/geometría, se informa separado y **no se reproyecta**.

Hay 814 equivalencias activas y 2.082 recorridos/sentidos en catálogo; ningún
destino de las equivalencias falta en el catálogo. El raw sólo contiene
posiciones vinculadas por la ingesta: las identidades del feed sin equivalencia
no están en esta población y no se pueden auditar desde esta tabla.

## Método y permisos

Herramienta: `scripts/audit-h0012-global.sql`; resultados completos agregados en
`.local/h0012/audit-global-retained-full.json` y
`.local/h0012/audit-global-original-window.json` (sin versionar). No expone IDs de
unidades ni posiciones individuales. Radio fijo aprobado **100 m**, distancias
PostGIS geography con esferoide; ninguna equivalencia ni filtro fue cambiado.

La búsqueda directa contra el catálogo completo superó el timeout de cinco
minutos. El usuario autorizó un índice espacial **TEMP** de fragmentos:

- La estructura TEMP y su índice se crean antes de abrir el snapshot; no se leen
  fuentes durante ese paso, pues PostgreSQL prohíbe CREATE TABLE en READ ONLY.
- Las fuentes se leen en una transacción **repeatable-read/read-only**. Dentro
  de ella PostgreSQL permite escribir únicamente las filas de la tabla TEMP.
- Cada fragmento tiene hasta 128 vértices originales; el extremo común se
  conserva entre fragmentos. No se interpola ni se corta una arista geodésica.
- Se crearon 7.017 fragmentos, preservando las **756.932 aristas originales**.
  La suma de longitudes de catálogo y fragmentos difiere en menos de 0,001 m
  por acumulación numérica. El radio exacto y la geodesia no cambian.
- El reporte registra `sourceTransactionReadOnly`, `fragmentIntegrity` y los
  filtros. La búsqueda completa terminó en aproximadamente **51,7 s**.
- ROLLBACK elimina las filas TEMP de esa transacción; al cerrar la sesión
  desaparecen también tabla e índice. No hay DDL ni DML sobre tablas persistentes.

Con `compare_catalog=false` (default) se realiza sólo la comparación con el
shape asignado; los conteos contra otros shapes son **null**, no cero. La corrida
básica tomó unos 9,5 s. Sus conteos primarios, distribución y estados coinciden
exactamente con la corrida completa.

## Población retenida analizada

Ventana UTC fija:
`[2026-10-08T17:01:10Z,2026-10-11T00:46:52.000001Z)`.

- **515.592 posiciones** de **7.220 unidades observadas**.
- **629 identidades GCBA/sentido** de las 814 equivalencias activas.
- **563 destinos GTFS/ramal/sentido**, con 145 etiquetas públicas de línea.
- Todas las posiciones de esta ventana son comparables con la equivalencia y
  geometría actuales; no hay pérdida por igualdad indebida de IDs, mapping
  cambiado o hash histórico. Esto verifica consistencia con la tabla, **no la
  exactitud operativa de cada equivalencia**.

Sólo hay GPS en dos fechas locales, no tres fechas completas:

| Fecha local (Buenos Aires) | GPS UTC disponible | Posiciones | Fuera de shape | Sin ningún shape próximo |
|---|---|---:|---:|---:|
| 2026-10-08 | 17:01:10 hasta 2026-10-09 00:36:50 | 299.065 | 39.572 | 8.632 |
| 2026-10-10 | 15:05:10 hasta 2026-10-11 00:46:52 | 216.527 | 29.546 | 6.795 |

No hay GPS del 09/10 local en esta ventana. Primer/último timestamp no demuestra
captura continua entre ellos; no atribuir ausencia de datos a ausencia de servicio.

## Resultados globales

| Situación | Posiciones | Sobre raw comparable |
|---|---:|---:|
| Dentro de 100 m del shape asignado | **446.474** | **86,59%** |
| Fuera del shape asignado, pero cerca de otro shape | **53.691** | **10,41%** |
| Fuera del shape asignado y sin ningún shape a 100 m | **15.427** | **2,99%** |
| **Total fuera del shape asignado** | **69.118** | **13,41%** |

Entre las exteriores, el 77,68% tiene otro shape próximo. De las 69.118
exteriores, 43.864 reportan velocidad instantánea >0; de las 15.427 sin ningún
shape próximo, 9.760 reportan speed>0. Esto no demuestra servicio comercial ni
movimiento sostenido y no se usa para excluir posiciones.

La presencia de exteriores es extendida:

- **612 de 629 identidades GCBA/sentido** tienen al menos una exterior.
- **547 de 563 recorridos/ramales/sentidos GTFS** tienen al menos una exterior.
- Se observan exteriores en 141 de las 145 etiquetas públicas de línea.
- La mediana de la fracción exterior por identidad GCBA/sentido es **11,28%**;
  percentil 90 **36,60%**. Son proporciones por identidad, no ponderadas por raw.
- Los diez destinos con más posiciones exteriores concentran sólo **16,43%**
  de las exteriores. No es un fenómeno explicado por una única línea.

**Una sola posición exterior no invalida una línea ni su equivalencia.** Los
porcentajes por posición no representan fracción de viajes completos perdidos,
pasajeros ni cobertura territorial/fleet del AMBA.

### Algunos destinos en la ventana ampliada

Listado descriptivo de calidad de datos, **no ranking de cuellos de botella**:

| Línea/ramal/sentido | Raw | Exteriores | Proporción |
|---|---:|---:|---:|
| 622/R3/0 | 4.499 | 1.624 | 36,10% |
| 65/B/0 | 4.075 | 1.533 | 37,62% |
| 622/R3/1 | 3.614 | 1.357 | 37,55% |
| 132/A/1 | 3.949 | 1.098 | 27,80% |
| 132/A/0 | 3.463 | 1.039 | 30,00% |
| 79/H/0 | 1.595 | 955 | 59,87% |
| 194/F/0 | 5.882 | 954 | 16,22% |
| 79/H/1 | 1.490 | 943 | 63,29% |
| 60/I/1 | 4.122 | 935 | 22,68% |
| 194/C/1 | 3.680 | 915 | 24,86% |

## Control con la ventana original

También se ejecutó sobre
`[2026-10-09T23:50:52.000001Z,2026-10-10T23:50:52.000001Z)` (unos 17 s):

- 142.268 posiciones, 579 identidades GCBA/sentido, 522 destinos GTFS/sentido.
- 123.083 dentro y 19.185 fuera del asignado (**13,49%** exteriores).
- 14.874 exteriores cerca de otro shape; 4.311 sin ningún shape cercano.
- Cierra exactamente con los resultados previos de los tres recorridos y con
  el conteo espacial del spike. El resultado general no depende de confundir IDs.

## Verificación y reproducción

```sh
psql "$DATABASE_URL" -X -qAt -v compare_catalog=true \
  -v from='2026-10-08T17:01:10Z' -v to='2026-10-11T00:46:52.000001Z' \
  -f scripts/audit-h0012-global.sql > .local/h0012/audit-global-retained-full.json
python3 scripts/test-h0012-global.py
```

`test-h0012-global.py` pasó en PostGIS con tablas fuente TEMP aisladas: IDs
GCBA/GTFS distintos y colisión deliberada de un ID GCBA con otro ID GTFS,
sentidos distintos, mapping histórico, hash viejo, equivalencia/destino ausentes,
ventana semiabierta, catálogo próximo/no próximo y raw vacío. Un zigzag de 301
vértices comprueba extremos compartidos, última arista, conteo de aristas y
longitud de fragmentos. También se verificó paridad del análisis básico/completo
sobre los 515.592 registros reales. Sin cambios de Go en esta etapa.

## Conclusión antes de decidir

La pérdida de correspondencia espacial es **general dentro de la población
observada**, aunque mayor en algunos ramales. La mayor parte del raw (86,59%)
es compatible espacialmente con el destino de la tabla H0011; eso no garantiza
exactitud de progresivas en lazos ni pasajes completos.

La proximidad a otro recorrido no distingue ruido GPS, calzada paralela,
variante/sentido, recorrido fuera de servicio o mapping incorrecto. **No justifica
reasignar posiciones ni mappings automáticamente.** Las posiciones sin catálogo
próximo tampoco se recuperan sólo cambiando equivalencias dentro del radio actual.

Antes de definir publicación o cambiar mappings se necesita revisar sectores y
trayectorias de los casos más afectados, y medir cuánto de esta pérdida fragmenta
pasajes completos. Las franjas y días observados deben acompañar cualquier
conclusión de recurrencia. Ninguna política nueva de exclusión, radio o ranking
se implementó a partir de estos resultados.
