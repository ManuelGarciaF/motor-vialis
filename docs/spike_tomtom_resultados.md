# Resultados del spike TomTom Traffic Flow

Fecha de ejecución: 2026-09-12.

## Objetivo

Comparar zooms y medir si el matching espacial simple entre TomTom Traffic Flow
y `vialis.calles` alcanza para RF05, sin asumir identificadores compartidos.

El ejecutable reproducible está en `cmd/tomtom-spike`.

## Método

- Endpoint: Traffic Flow Vector Tiles v4, tipo `absolute`.
- Margen TomTom: 0,1.
- Área de cada muestra: radio geográfico de 1 km.
- Grafo: carga activa de `vialis.calles`.
- Candidato: segmento TomTom a hasta 15 m de la arista.
- Compatibilidad: diferencia angular no orientada de hasta 45°.
- Selección: menor distancia y orden estable del feature.
- Las métricas cuentan aristas cuya geometría alcanza el círculo de prueba.
- `nearby` significa que existe alguna geometría TomTom a 15 m sin aplicar el
  filtro angular; incluye cruces perpendiculares y por eso es sólo un techo, no
  un match válido.

Los tiles usan margen y pueden repetir features en bordes. Eso altera el conteo
bruto de features, pero no el porcentaje de aristas del grafo que encuentra un
match.

## Comparación de zoom en CABA

Muestra centrada en el Obelisco (`-34.6037, -58.3816`): 590 aristas y 57,8 km
de calles dentro del radio.

| Zoom | Tiles | Bytes | Features | Aristas con match | Longitud con match | Distancia p50 | Distancia p95 | Latencia fría |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 14 | 4 | 19–20 KB | 215 | 92,7 % | 91,3 % | 0,30 m | 3,30 m | 1,29 s |
| 15 | 9 | 18–19 KB | 206 | 92,7 % | 91,3 % | 0,39 m | 3,24 m | 1,61 s |
| 16 | 25 | 23–24 KB | 286 | 92,9 % | 91,3 % | 0,40 m | 3,29 m | 4,23 s |

No hubo una mejora material de cobertura al subir el zoom. Las vías primary,
tertiary y trunk obtuvieron 100 % de match en z14; residential obtuvo 95,5 %.

## Muestras adicionales en z14

| Zona | Tiles | Aristas | Cerca a 15 m | Aristas con match | Longitud con match | Distancia p95 |
|---|---:|---:|---:|---:|---:|---:|
| Obelisco | 4 | 590 | 99,7 % | 92,7 % | 91,3 % | 3,30 m |
| Morón | 4 | 669 | 81,3 % | 58,6 % | 59,8 % | 4,18 m |
| La Plata | 4 | 782 | 94,6 % | 76,9 % | 74,6 % | 6,62 m |

Como perder aristas puede fragmentar el grafo de forma no lineal, se agregó una
segunda medición con `pgr_connectedComponents`:

| Zona | Vértices todavía cubiertos | Mayor componente cubierto sobre el grafo original | Componentes resultantes |
|---|---:|---:|---:|
| Obelisco | 98,7 % | 98,1 % | 2 |
| Morón | 70,3 % | 68,5 % | 3 |
| La Plata | 89,0 % | 88,1 % | 3 |

El porcentaje de la tercera columna es la fracción de vértices originales que
permanece en una única componente conectada usando sólo aristas con tráfico; es
más representativo de la capacidad de encontrar un desvío que contar aristas.

En Morón se repitió la muestra en z15: 9 tiles, 58,4 % de aristas y 59,5 % de
longitud con match. El zoom mayor no agregó cobertura útil.

## Barrido completo de CABA en z14

Se descargaron exactamente los 68 tiles que intersectan el polígono oficial de
CABA y se compararon contra las 34.990 aristas —3.409,5 km— de
`vialis.calles` que lo alcanzan. Cuatro tiles ya estaban cacheados, por lo que el
barrido produjo 64 requests nuevos.

| Métrica | Resultado |
|---|---:|
| Aristas con match | 57,9 % |
| Longitud con match | 58,6 % |
| Vértices todavía cubiertos | 72,5 % |
| Mayor componente cubierta sobre vértices originales | 71,7 % |
| Componentes resultantes | 56 |
| Distancia de matching p50 | 0,41 m |
| Distancia de matching p95 | 4,82 m |

Cobertura de longitud por clase OSM:

| Clase | Cobertura |
|---|---:|
| trunk | 100,0 % |
| motorway_link | 98,3 % |
| primary | 97,8 % |
| motorway | 96,5 % |
| secondary | 95,5 % |
| tertiary | 82,4 % |
| primary_link | 80,7 % |
| residential | 41,1 % |
| living_street | 52,5 % |

La muestra de 1 km del Obelisco no era representativa de toda CABA: el centro
tiene cobertura mucho mejor. La pérdida global se concentra en calles
residenciales, mientras que la red arterial por donde circulan normalmente los
colectivos conserva entre 82 % y 100 % de su longitud.

## Estimación por calles cercanas compatibles

Se prototipó una estimación para aristas sin match directo: mediana de hasta 5
segmentos TomTom a 300 m, misma `road_category`, con un mínimo de 3 muestras. Se
excluyen cierres y velocidades no positivas.

Para validarla se ocultó la fuente directa de cada arista conocida y se comparó
la mediana vecina contra su velocidad TomTom real. No hubo requests adicionales:
los 68 tiles salieron de caché.

| Métrica                                    | CABA completa | Obelisco 1 km |
|--------------------------------------------|--------------:|--------------:|
| Aristas sin match que pudieron estimarse   |        58,4 % |        93,0 % |
| Longitud directa o estimada                |        83,3 % |        99,9 % |
| Vértices directos o estimados              |        88,5 % |       100,0 % |
| Mayor componente sobre vértices originales |        88,0 % |       100,0 % |
| Cobertura del holdout                      |        56,8 % |        74,3 % |
| Error absoluto medio                       |      6,5 km/h |      3,2 km/h |
| Error absoluto mediano                     |        5 km/h |        5 km/h |
| Error absoluto p90                         |       15 km/h |        5 km/h |
| Estimaciones a ±10 km/h                    |        87,2 % |        98,3 % |
| Sesgo medio                                |     +1,2 km/h |     -0,1 km/h |

La estimación recupera buena parte de la conectividad y es prometedora para
ordenar alternativas, especialmente en el centro. No tiene precisión suficiente
para presentarse como velocidad observada ni como ETA independiente. El holdout
sólo mide calles donde TomTom sí tiene verdad conocida; supone que las calles
omitidas se comportan de forma similar a las cubiertas de su categoría.

Por clase OSM en las muestras locales:

- Morón: primary y secondary 100 %, tertiary 98,1 %, residential 40,6 %.
- La Plata: secondary 97,8 %, primary 82,2 %, residential 74,4 %, tertiary
  39,4 %.

## Validación del sentido de `one_side`

La documentación de TomTom define `one_side` como tráfico de un solo lado de
una vía bidireccional, pero no declara en forma explícita que el orden del
`LineString` sea el sentido de circulación. Se contrastó esa hipótesis contra
las aristas OSM de sentido único de CABA, cuya geometría está normalizada en el
sentido permitido.

Elegir primero el segmento más cercano y mirar después su orientación no
funciona: entre 707 matches `one_side` seleccionados de esa manera, sólo 47,1 %
apuntaba en el sentido OSM. El segmento más próximo puede representar la mano
opuesta de la misma vía.

Al buscar candidatos por cada sentido antes de elegir por distancia, el
resultado cambia:

| Métrica | CABA completa |
|---|---:|
| Aristas OSM de sentido único con algún candidato `one_side` | 1.392 |
| Con candidato orientado en el sentido permitido | 1.302 (93,5 %) |
| Con candidatos en ambos sentidos dentro de 15 m | 1.162 |
| Sólo con candidato opuesto | 90 |

También se probó inferir el sentido por el lado lateral de la geometría respecto
del eje OSM. No resultó robusto: de 2.424 aristas bidireccionales con candidatos
`one_side`, sólo 134 tenían geometrías distinguibles a ambos lados y 346 tenían
todos los candidatos a menos de 0,75 m del eje.

La política resultante es hacer matching **por costo dirigido**:

- `one_side` sólo compite para el costo cuya orientación coincide;
- el costo opuesto busca su propio candidato;
- `full` puede abastecer ambos costos permitidos por OSM;
- nunca se selecciona primero un match no orientado para luego copiarlo;
- si un sentido no tiene candidato directo, sólo puede entrar mediante la
  estimación vecina documentada o queda fuera del grafo.

El 6,5 % de aristas de sentido único que sólo encontró candidatos opuestos se
trata como falta de cobertura o diferencia de cartografía, no se corrige
invirtiendo el tráfico.

## Conclusiones

1. **Zoom 14 es suficiente para el MVP.** En las comparaciones realizadas, z15
   y z16 multiplicaron requests sin mejorar el matching. Se adopta z14 y se
   conserva como constante versionada.
2. **No hace falta un matcher ponderado.** Las distancias p50 menores a 1 m y
   p95 menores a 7 m confirman que, cuando ambos proveedores publican la calle,
   sus geometrías están muy próximas.
3. **El filtro angular simple sigue siendo necesario.** La proximidad sola
   incluye features que únicamente cruzan la arista en una intersección y
   podría asignar la velocidad de una calle perpendicular.
4. **La limitación principal es cobertura de tráfico, no precisión geométrica.**
   TomTom no publica todos los tramos residenciales de las muestras suburbanas,
   y subir el zoom no los recuperó.
5. **El centro de CABA y sus arterias tienen buena cobertura, pero toda CABA no
   llega al 90 %.** En el Obelisco, 98,1 % de los vértices originales sigue en
   la componente principal. En el barrido completo, sólo 71,7 % permanece en la
   mayor componente porque TomTom omite muchas calles residenciales.
6. **La gravedad debe medirse sobre recorridos y cortes reales.** La cobertura
   arterial sugiere que muchos casos serán resolubles, pero un corte que obligue
   a entrar en la trama residencial puede fallar incluso en CABA.
7. Con la política acordada, una arista sin match queda fuera del grafo. Si eso
   impide formar el desvío, RF05 debe devolver
   `traffic_coverage_insufficient`, no inventar velocidad ni caer a distancia.

La buena cobertura de vías principales es prometedora para recorridos de
colectivo y especialmente para la zona central. La cobertura residencial menor
puede afectar rodeos por calles locales y debe medirse con casos reales de
líneas antes de cerrar el MVP.

## Consumo y reutilización

Para producir la comparación definitiva se hicieron 55 requests de tiles:

- 38 para z14/z15/z16 en CABA;
- 8 para Morón y La Plata en z14;
- 9 para comparar Morón en z15.

Representan 0,0275 % del cupo mensual de 200.000 requests. Antes de incorporar
la caché se había ejecutado una descarga exploratoria de 38 tiles; el total de
toda la sesión fue entonces **93 requests**, o 0,0465 % del cupo mensual.

El barrido completo de CABA agregó 64 requests: 68 tiles requeridos menos 4 ya
cacheados. El total de toda la sesión quedó en **157 requests**, o 0,0785 % del
cupo mensual.

Los PBF reutilizables quedaron únicamente en `/tmp/tomtom-spike-tiles`, fuera
del repositorio. `-output-dir` funciona como caché read-through: las repeticiones
leen esos archivos y no consumen TomTom. Una repetición completa confirmó 38/38
cache hits y tardó menos de 150 ms por zoom incluyendo el matching PostgreSQL.
