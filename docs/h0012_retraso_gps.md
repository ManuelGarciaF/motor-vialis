# H0012 — Antigüedad GPS y cierre por falta de actualizaciones

## Decisiones del usuario

- Persistir **pasajes y metadatos de calidad**, no un archivo adicional de GPS.
  La calibración posterior se limita a lo recuperable de esos pasajes; no se
  podrán volver a medir con otros límites de sector/matching después de la purga.
  No se modifica la retención raw existente de H0011.
- Arrancar con **datos nuevos**, sin backfill del histórico de viabilidad.
- Tras revisar los datos, el usuario eligió **4 capturas correctas y no parciales
  consecutivas sin un timestamp GPS nuevo de la unidad** para cerrar la medición
  pendiente como incompleta. Errores de captura no cuentan. Misma coordenada con
  timestamp nuevo sí es actualización y reinicia el contador.

Estas son decisiones para implementar; no se cambió todavía el ciclo del worker,
la base persistente ni los parámetros H0011.

## Qué se midió

Consultas sólo lectura, sin PostGIS ni salida de coordenadas/IDs de unidades:

- `scripts/audit-h0012-gps-delay.sql`: antigüedad = `recibido_en - observado_en`
  en la primera recepción **persistida** de cada vehículo/timestamp.
- `scripts/audit-h0012-update-gaps.sql`: sensibilidad temporal de cerrar una
  secuencia después de 3 o 4 capturas no parciales sin un nuevo GPS.

Cada consulta tardó unos **3,5 s**, incluyendo arranque de herramienta. Usan
snapshots repeatable-read/read-only y límite de statement de 30 s. Las dos
consultas son snapshots separados, con ingesta activa; no se asumen iguales
cohortes entre ellas. Archivos locales: `.local/h0012/gps-delay.json` y
`.local/h0012/update-gaps.json`.

La primera se tomó el **2026-10-14T23:09:54Z**: ahora hay más datos retenidos que
en el análisis de cobertura anterior de 515.592 GPS. Sus conclusiones siguen
correspondiendo a aquella ventana fija, no a toda la población actual.

## Antigüedad en primera recepción

**1.137.627 GPS canónicos** retenidos, observados entre 08/10 y 14/10:

| Medida | Antigüedad |
|---|---:|
| Mediana | **67,0 s** |
| Percentil 90 | 122,5 s |
| Percentil 95 | **145,7 s** |
| Percentil 99 | **196,3 s** |
| Máximo | 25.303,0 s |

569 registros superaban 300 s; ninguno tenía reloj adelantado. El perfil de
antigüedad no negativa hasta 300 s usa el límite existente H0011 sólo como corte
descriptivo: no incorpora un filtro nuevo a H0012.

En la última hora de recepciones de ese snapshot: 136.238 GPS, mediana 79,2 s,
p95 172,3 s, p99 182,0 s. No es una medición de latencia de red pura: incluye
antigüedad del dato en el proveedor, frecuencia de sondeo y recepción. Tampoco
mide cuánto tiempo lleva detenido un vehículo.

El raw deduplica vehículo/timestamp: no conserva cada reaparición del mismo GPS
en todas las capturas. En 592 capturas hubo 151.408 filas stale de 2.784.668
válidas, incluyendo repeticiones/no vinculadas; ese denominador no es el de los
GPS canónicos. No usar la escasa proporción stale del raw como prueba de que los
snapshots casi nunca traen ubicaciones viejas.

Hubo 87 GPS anteriores a un timestamp ya recibido de la misma unidad, con máximo
retroceso temporal de 14 s. Antigüedad y llegada fuera de orden son conceptos
distintos; estos datos no garantizan un máximo futuro de retraso.

## ¿Qué implica cerrar tras varias capturas?

En el segundo snapshot, **2026-10-14T23:12:21Z**, la última hora tenía 41 capturas:
mediana del intervalo 75,9 s y p95 197,6 s. Tres intervalos de captura tenían
mediana **242,4 s (4 min 2 s)** y p95 364,4 s (6 min 4 s).

**Tres capturas no equivalen a tres minutos.** Se cuentan capturas recibidas,
no ejecuciones H0012 ni intervalos nominales del scheduler. Los huecos históricos
incluyen pausas de horas/días: no permiten asumir captura continua.

Se revisaron pares consecutivos en orden de recepción, misma identidad/version,
ambos con antigüedad entre 0 y 300 s y separación GPS positiva de hasta 180 s.
Sólo se comprobó compatibilidad temporal, **no un pasaje espacial válido**.

| Población | Pares temporalmente compatibles | Llegaron después de ≥3 capturas no parciales sin novedades de la unidad | Después de ≥4 |
|---|---:|---:|---:|
| Retenido en ese snapshot | 989.762 | **17** | **0** |
| Última hora de ese snapshot | 122.819 | 11 | 0 |

Las capturas no parciales intermedias se cuentan antes de la actualización que
reaparece; esa actualización no se cuenta como ausente. Las parciales no suman.
No se puede reconstruir desaparición literal del snapshot a partir del raw
canónico: esto estudia **ausencia de nuevos timestamps**, no ausencia de filas.

## Regla acordada y límites

Se acordó **4**, en lugar de los 3 del ejemplo inicial, para evitar los casos
compatibles observados, a costo de esperar una captura adicional. No es garantía
de que nunca aparezca información útil después de cerrar.

- Procesar las nuevas ubicaciones antes de contar ausencias de esa captura.
- Fallos y capturas parciales no incrementan el contador. El tratamiento de
  presencia en una parcial no debe descartar automáticamente un GPS válido:
  no incrementar ausencia no significa ignorar datos disponibles.
- Distinguir timestamp nuevo de cambio de coordenada: un colectivo quieto que
  actualiza su timestamp sigue teniendo observaciones.
- Cerrar por ausencia como **incompleto**, sin calcular un tiempo hasta el cierre
  ni atribuir lentitud. Esta regla **no amplía el máximo hueco de soporte GPS de
  180 s** y no reemplaza cortes por retroceso, cambio de identidad o ambigüedad.
- La frecuencia de ejecución H0012 queda pendiente de medir costo incremental.
  Si procesa varias capturas juntas debe conservar el orden y contar capturas,
  no incrementar una sola vez por ejecución ni varias por reintento.
- La regla de cierre por falta de actualizaciones no define todavía si un GPS
  tardío puede corregir un resultado ya finalizado. Esa política y el estado
  mínimo para recuperación tras reinicio siguen por especificar.

## Reproducción y tests

```sh
# Credenciales cargadas sin imprimirlas.
psql "$DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 \
  -f scripts/audit-h0012-gps-delay.sql > .local/h0012/gps-delay.json
psql "$DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 \
  -f scripts/audit-h0012-update-gaps.sql > .local/h0012/update-gaps.json
TEST_DATABASE_URL="$DATABASE_URL" python3 scripts/test-h0012-gps-delay.py
```

Los tests usan únicamente fixtures TEMP de sesión y consultas contra ellas;
no escriben en `vialis`. Verifican edades/buckets, el orden de conteo de
actualización/ausencia, omisión de parciales y un caso sintético que demuestra
que cuatro capturas tampoco garantizan ausencia de llegadas posteriores.
