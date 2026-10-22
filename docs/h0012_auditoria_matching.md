# H0012 — Auditoría inicial de falta de correspondencia GPS

Continuación de [h0012_analisis.md](h0012_analisis.md), por decisión del usuario:
auditar causas antes de continuar, sin relajar filtros.

Herramienta: `scripts/audit-bottlenecks-matching.sql`. Transacción read-only,
repeatable-read, timeout 5 min; no cambia datos, equivalencias ni filtros.
Salida agregada sin IDs de unidades ni coordenadas GPS individuales.

```sh
psql "$DATABASE_URL" -X -qAt \
  -v line_id=1015 -v from='2026-10-09T23:50:52.000001Z' \
  -v to='2026-10-10T23:50:52.000001Z' \
  -f scripts/audit-bottlenecks-matching.sql > .local/h0012/audit-1015.json
```

Se ejecutó para IDs 1015, 1155 y 1719 con la misma ventana del análisis, radio
100 m. Todas las posiciones de estas muestras conservan el hash y shape vigentes.

## Causa inmediata comprobada

| Recorrido | Raw | Dentro de 100 m del shape | Fuera de 100 m | Unmatched en el análisis |
|---|---:|---:|---:|---:|
| 622/R3/0 | 1.405 | 814 | 591 | 591 |
| 53/C/0 | 1.387 | 1.280 | 107 | 107 |
| 194/F/0 | 1.237 | 1.003 | 234 | 234 |

Los unmatched se explican exactamente por distancia geodésica al shape. No se
pierden candidatos interiores por una versión vieja, el filtrado de mínimos
locales o incompatibilidad temporal en estas tres muestras. Las observaciones
ambiguas se cuentan aparte; no se convierten en unmatched por elegir mal un
ancla en estas corridas. Esto valida la contabilidad aquí, no universalmente.

En 622/R3:

- 42,1% del raw está fuera del radio.
- 307 posiciones están a más de **2 km** del shape; máximo **17,22 km**.
- De las 591 exteriores, 300 reportan velocidad instantánea >0. No se puede
  explicar todo como vehículos quietos; tampoco eso demuestra servicio comercial.
- Las 1.405 posiciones pertenecen a una sola fecha local observada. La ventana
  solicitada de 24 h no garantiza que haya capturas durante todas sus horas;
  todavía no permite verificar recurrencia en fechas distintas.

## Equivalencias que requieren revisión

La carga versionada `sql/recorridos/cargar_equivalencias_gcba.sql` asigna cuatro
identidades GCBA al destino GTFS `2699/0` (622R3). El CSV administrado
`docs/h0011_spatial_match_candidates.csv` conserva estos antecedentes:

| GCBA/sentido | Nombres de la fuente en CSV | Clasificación del mapping | Raw actual | Fuera de radio | Exterior con speed>0 | Distancia máxima |
|---|---|---|---:|---:|---:|---:|
| 1149/0 | 46B | high_confidence | 125 | 61 | 5 | 678 m |
| 2080/0 | 247-7 | ambiguous | 661 | 283 | 170 | 4.222 m |
| 720/0 | 506R80B | ambiguous | 426 | 218 | 99 | 17.221 m |
| 989/0 | 8A | ambiguous | 193 | 29 | 26 | 1.558 m |

Los nombres discrepantes y las clasificaciones son señales para revisión, **no
identifican automáticamente la equivalencia correcta**. Posiciones fuera de
servicio, variantes no representadas por el shape canónico, errores del feed o
mapping incorrecto siguen siendo hipótesis. Hace falta comparar trayectorias y
catálogo/fuente antes de atribuir la causa raíz o cambiar el mapping.

## Conclusión y límite de la auditoría

No hay evidencia para ampliar el radio sólo para aumentar cobertura. Absorber
puntos a kilómetros del recorrido puede asignar tiempos a sectores equivocados.
El cálculo conservador está haciendo explícita una incompatibilidad espacial
real con la geometría asignada, cuya causa raíz requiere una revisión de datos.

La auditoría se ejecutó correctamente contra la base real y sus conteos cierran
con los del análisis; `git diff --check` sin errores. Ningún cambio a H0011,
esquema o datos persistentes. El usuario autorizó la revisión comparativa que
sigue, **sin modificaciones**.

## Revisión de equivalencias contra el catálogo completo

Herramienta adicional `scripts/audit-bottlenecks-equivalences.sql`, misma ventana y
radio 100 m; read-only/repeatable-read, sin generar reemplazos. Compara todas las
posiciones de las cuatro identidades con todos los shapes GTFS, incluidos ambos
sentidos. Conserva hasta diez candidatos por identidad y siempre el asignado.
Orden exploratorio: cantidad de posiciones próximas, positivas entre ellas,
mediana de distancia e ID. No es un score aprobado para cambiar mappings.

```sh
psql "$DATABASE_URL" -X -qAt -v line_id=1015 \
  -v from='2026-10-09T23:50:52.000001Z' -v to='2026-10-10T23:50:52.000001Z' \
  -f scripts/audit-bottlenecks-equivalences.sql > .local/h0012/equivalences-1015.json
```

| GCBA | Posiciones | Próximas al GTFS asignado | Mejor candidato puramente espacial | Cobertura de ese candidato |
|---|---:|---:|---|---:|
| 1149/0 | 125 | 64 | 622/R3/0 (ID 1015) | 51,2% |
| 2080/0 | 661 | 378 | 622/R3/1 (ID 1016) | 61,0% |
| 720/0 | 426 | 208 | 622/R3/1 (ID 1016) | 49,3% |
| 989/0 | 193 | 164 | 622/R3/0 (ID 1015) | 85,0% |

**No aparece un reemplazo claramente superior que explique toda la secuencia.**
Para 2080 el sentido contrario añade sólo 25 posiciones próximas, y para 720
cuatro; cercanía al sentido contrario no prueba dirección de circulación. Por
eso no se propone invertir automáticamente los sentidos.

Entre las 591 posiciones fuera del shape asignado:

| GCBA | Próximas a otro shape | Sin ningún shape del catálogo a 100 m |
|---|---:|---:|
| 1149/0 | 14 | 47 |
| 2080/0 | 133 | 150 |
| 720/0 | 87 | 131 |
| 989/0 | 26 | 3 |
| **Total** | **260** | **331** |

De las 331 sin ningún shape próximo, 145 reportan speed>0. Cambiar solamente el
mapping no puede recuperar esas posiciones dentro del radio actual. Las demás
260 se distribuyen entre corredores compartidos: el solapamiento no identifica
por sí solo una línea o ramal correcto.

### Interpretación y límites

Las equivalencias actuales continúan entre los mejores candidatos espaciales;
esto debilita la hipótesis de un simple reemplazo de destino, pero no demuestra
que el mapping sea correcto. Siguen abiertas variantes faltantes, operación
fuera de servicio, discrepancias de catálogo/feed o metadata incorrecta.

Esta comparación de las posiciones de trayectorias no valida viajes completos,
orientación o permanencia sobre una alternativa: no transforma posiciones GPS
en votos independientes ni selecciona automáticamente otra identidad. Las
posiciones detenidas pueden dominar conteos; se exponen también counts de speed
positivo, sin excluir observaciones por velocidad. Hace falta evidencia
independiente o revisión de trayectorias completas para una corrección definitiva.

La consulta se ejecutó correctamente contra PostGIS real, sin modificaciones ni
relajación de filtros. **No hay evidencia suficiente para aplicar cambios de
mapping.** Tampoco se ha definido aún una política de publicación para recorridos
con cobertura insuficiente o cómo tratar posiciones fuera de catálogo. Esas
políticas y los umbrales de recurrencia requieren consulta antes de implementarse.
