# Plan de implementación de RF05 — desvíos temporales

## 1. Objetivo del MVP

Agregar al motor la capacidad de recibir una ruta válida y un único corte vial,
generar una variante operable dentro de un área local, elegirla con tráfico
actual y comparar su impacto contra la ruta original.

El MVP admite:

- un solo corte por consulta;
- corte GeoJSON `LineString` en WGS 84;
- calles bloqueadas sobre `vialis.calles`;
- tráfico TomTom Traffic Flow Vector Tiles;
- los criterios `MENOR_TIEMPO` y `MENOR_PARADAS_PERDIDAS`;
- omisión de paradas a hasta 500 m del corte;
- búsqueda limitada a 1 km del corte;
- evaluación final con el simulador existente (demanda, distancia, tiempo GTFS
  y recaudación).

No incluye múltiples cortes, persistencia, edición manual, parada de reemplazo,
`MENOR_DESVIO` ni fallback sin tráfico.

La semántica funcional autoritativa sigue en
[`arquitectura_motor.md`](arquitectura_motor.md), sección 13. Este documento
ordena su implementación.

## 2. Decisiones cerradas

### 2.1. Corte

El request contiene exactamente un `LineString`. Se espera que provenga de un
path calculado con OSRM y, por lo tanto, siga geometrías OSM. Aun así, no se
exige igualdad exacta: el extracto de OSRM y el PBF con el que se construyó
`vialis.calles` pueden corresponder a fechas distintas.

El corte se valida antes de consultar TomTom o PostgreSQL:

- GeoJSON válido y tipo `LineString`;
- al menos dos posiciones distintas;
- coordenadas finitas dentro de latitud/longitud válidas;
- sin segmentos de longitud cero;
- intersección con el alcance activo de `vialis.calles_metadata`;
- cantidad de puntos y longitud total bajo los límites defensivos del request.

Los valores iniciales propuestos son 10.000 posiciones y 20 km. Además se
rechaza una consulta que requiera más de 32 tiles, aunque tenga un solo corte.
Los tres límites deben quedar como constantes versionadas y ajustarse con datos
de uso.

### 2.2. Radios

| Parámetro | Valor inicial | Función |
|---|---:|---|
| Corredor prohibido | 5 m | Toda arista que entre en este buffer del corte queda bloqueada. |
| Tolerancia de parada alcanzada | 20 m | Una parada a esta distancia o menos queda forzosamente no disponible. |
| Radio de omisión | 500 m | Una parada a esta distancia o menos del corte puede omitirse. |
| Radio de búsqueda | 1.000 m | El camino nuevo y los tiles quedan limitados a este buffer. |

Acá un **buffer** es el polígono formado por todos los puntos que están a una
distancia máxima del `LineString`: visualmente, un corredor que acompaña el
corte y tiene extremos redondeados. "Geográfico" significa que el ancho se
expresa en metros sobre la Tierra, no en grados de latitud/longitud.

En PostGIS el corte llega como `geometry(LineString, 4326)` y se convierte a
`geography` para medir o expandirlo. Por ejemplo:

```sql
ST_Buffer(corte::geography, 5)::geometry     -- corredor prohibido
ST_Buffer(corte::geography, 1000)::geometry  -- área de búsqueda
```

No hace falta materializar polígonos de 20 y 500 m para clasificar las paradas
del request; se puede medir directamente:

```sql
ST_DWithin(parada.posicion::geography, corte::geography, 20)  -- forzada
ST_DWithin(parada.posicion::geography, corte::geography, 500) -- opcional
```

Por lo tanto, sólo se construyen dos buffers físicos por consulta: 5 m para
excluir aristas y 1 km para limitar el subgrafo, recortar la ruta y determinar
los tiles. No se amplía el radio si no aparece una solución.

La ruta exterior al buffer de 1 km se conserva. Sus intersecciones con el borde
del buffer son las anclas que se conectan mediante la red vial. Si el corte se
encuentra cerca de un extremo, la primera o última parada conservada puede hacer
de ancla.

### 2.3. Criterios

Los objetivos son lexicográficos:

1. `MENOR_TIEMPO`: minimiza segundos con tráfico actual y desempata por menor
   cantidad de paradas perdidas.
2. `MENOR_PARADAS_PERDIDAS`: maximiza paradas conservadas y desempata por menor
   cantidad de segundos con tráfico actual.

Una parada alcanzada directamente por el corte es forzosamente no disponible.
Las demás paradas a hasta 500 m son opcionales. Las que quedan fuera de ese
radio son obligatorias y mantienen su orden.

Consecuencia que debe probarse y mostrarse en la demo: sin una penalidad por
omitir paradas, `MENOR_TIEMPO` puede omitir todas las paradas opcionales si cada
omisión ahorra aunque sea pocos segundos. Ese comportamiento se desprende del
criterio acordado; no se agregará una penalidad implícita. Si resulta demasiado
agresivo, deberá definirse después un ahorro mínimo explícito.

### 2.4. Separación de modelos

- TomTom aporta costos actuales para **elegir** la variante.
- El estimador GTFS existente calcula el tiempo comercial para **evaluarla**.
- Los segundos TomTom no se copian a `simulation.Result` como tiempo GTFS.
- La respuesta del desvío debe exponer por separado el tiempo usado para la
  decisión y la edad del snapshot de tráfico.

### 2.5. Fallos de tráfico

No hay fallback silencioso. Se devuelve un error específico cuando:

- falta la API key;
- TomTom falla, limita o entrega un tile inválido;
- el snapshot excede el TTL admitido;
- la asociación espacial no brinda cobertura suficiente para construir un
  camino con costos actuales.

La ruta por distancia no puede presentarse como `MENOR_TIEMPO` ni como desempate
de `MENOR_PARADAS_PERDIDAS`.

## 3. Zoom y volumen de tiles

Un tile Web Mercator duplica su resolución lineal por cada nivel de zoom. Cerca
de la latitud de AMBA, su ancho aproximado es:

| Zoom | Ancho aproximado | Efecto esperado |
|---:|---:|---|
| 14 | 2,0 km | Pocos requests, más generalización y mayor riesgo de que falten calles locales. |
| 15 | 1,0 km | Balance inicial entre cobertura vial, matching y consumo. |
| 16 | 0,5 km | Más detalle, pero aproximadamente cuatro veces más tiles que z15 para la misma superficie. |

Bajar zoom no sólo reduce precisión geométrica. TomTom puede simplificar las
líneas y omitir categorías viales menores, lo que perjudica la asociación con
`vialis.calles`. La cuantización interna del MVT no es el problema principal:
con extent 4096 es submétrica en estos zooms; importan más la generalización y
qué calles publica TomTom en cada nivel.

El spike comparativo documentado en
[`spike_tomtom_resultados.md`](spike_tomtom_resultados.md) no encontró una mejora
material de cobertura al pasar de z14 a z15 o z16, ni siquiera en la muestra
suburbana de Morón. Por eso el MVP adopta **zoom 14**: para un corte corto cuyo
buffer mida aproximadamente 2 × 2 km suele requerir alrededor de 4 tiles, según
los bordes de la grilla. El zoom queda como constante versionada para poder
revisarlo si nuevas muestras contradicen el resultado.

## 4. Caché de tráfico

La caché es por `(proveedor, estilo, zoom, x, y)`, no por corte. De ese modo dos
cortes próximos reutilizan tiles aunque sus geometrías no sean idénticas.

Política del MVP:

- TTL máximo: **30 minutos**;
- guardar bytes PBF, instante de descarga y headers útiles (`ETag`, `Expires`);
- deduplicar solicitudes concurrentes de la misma clave;
- descargar en paralelo respetando el límite de TomTom, inicialmente 10 QPS;
- todos los tiles de una evaluación forman un snapshot lógico y la respuesta
  informa `fetchedAt`, edad máxima, zoom y si hubo hits de caché;
- caché en memoria por proceso, con capacidad acotada y reemplazo LRU;
- no persistir tiles en PostgreSQL durante el MVP.

Treinta minutos favorece consultas repetidas y la demo, pero permite decidir con
tráfico de hasta 30 minutos de antigüedad. La API debe llamarlo `trafficAge` y
no insinuar datos instantáneos. Una fase posterior puede respetar un TTL menor o
revalidar por `ETag` sin cambiar el dominio.

## 5. Asociación geométrica

### 5.1. Corte → calles bloqueadas

El `LineString` es una **barrera que no se puede atravesar**, no un selector de
la calle sobre la que fue dibujado. Por lo tanto, una calle perpendicular que
cruza el corte también debe quedar bloqueada. No se filtra por orientación ni
se exige que la arista acompañe longitudinalmente al corte.

La primera implementación debe:

1. construir alrededor del corte un corredor prohibido de 5 m para absorber las
   pequeñas diferencias entre las versiones OSM de OSRM y `vialis.calles`;
2. preseleccionar aristas por el envelope del corredor usando el índice GiST;
3. excluir todo costo dirigido cuya geometría interseque o ingrese al corredor,
   cualquiera sea su ángulo;
4. aplicar la exclusión antes de ejecutar pgRouting;
5. incluir los `id_calle` bloqueados en el resultado para trazabilidad.

El corredor también bloquea una arista longitudinal o paralela si ésta entra en
sus 5 m; una paralela que permanece completamente afuera sigue disponible. No
se propaga el bloqueo al resto del `osm_way_id` ni a una calzada vecina que no
intersecta el corredor.

Los 5 m son una hipótesis inicial deliberadamente acotada porque el corte viene
de OSRM. Se validan con LineStrings reales sobre calles simples, avenidas con
calzadas separadas, curvas e intersecciones. El MVP interpreta intersecciones en
2D: sin información de nivel adicional, también bloquea un cruce proyectado
entre puente y túnel.

### 5.2. TomTom → `vialis.calles`

Los tiles no traen `osm_way_id` ni `id_calle`, pero para el MVP no se necesita
un matcher ponderado ni una asignación global. Las geometrías deberían ser lo
suficientemente cercanas para resolver cada arista de `vialis.calles` con una
regla local y determinista:

1. buscar segmentos TomTom a hasta 15 m de la arista;
2. descartar segmentos claramente perpendiculares, usando una diferencia angular
   sin orientación mayor a 45°;
3. elegir el de menor distancia a la arista;
4. desempatar por un orden estable del feature dentro del tile;
5. convertir el tag `traffic_level` del tile `absolute` a costo:
   `costo_segundos = longitud_metros / (velocidad_km_h / 3,6)`;
6. tratar `road_closure=true` o velocidad no positiva como no transitable;
7. conservar `traffic_road_coverage` al asociar costos dirigidos. En particular,
   `one_side` no puede copiarse automáticamente al sentido opuesto. La relación
   entre orientación de la geometría, `full`/`one_side` y sentido se confirma
   con fixtures TomTom antes de implementarla.

No se utiliza una fórmula ponderada para el matching directo. La dirección
mínima sigue siendo necesaria para no asignar a una calle el tráfico de otra que
la cruza.

Cuando una arista no tiene match directo, el MVP puede estimar su velocidad sin
hacer otra llamada:

1. normalizar `vialis.calles.tipo` a la categoría TomTom equivalente;
2. buscar segmentos de esa misma `road_category` a hasta 300 m;
3. descartar cierres y velocidades no positivas;
4. tomar como máximo los 5 segmentos más cercanos y exigir al menos 3 fuentes;
5. usar la mediana de `traffic_level` como velocidad estimada.

El costo conserva su procedencia: `tomtom_direct` o
`tomtom_nearby_estimate`. La estimación no se presenta como observación de esa
calle y la respuesta informa por separado ambas coberturas. Si una arista no
tiene match ni tres muestras compatibles, queda fuera del grafo. Si eso impide
un camino, se responde `traffic_coverage_insufficient`.

El holdout inicial de CABA obtuvo un error absoluto medio de 6,5 km/h, mediana de
5 km/h y p90 de 15 km/h entre las aristas que pudieron estimarse; elevó la
cobertura de longitud de 58,6 % a 83,3 %. Los resultados completos están en
[`spike_tomtom_resultados.md`](spike_tomtom_resultados.md).

### 5.3. Ruta/paradas → grafo

No se usa el vértice más cercano. Cada punto se proyecta sobre una arista
dirigida compatible y se inserta como punto virtual con la fracción de la
arista, usando la familia `pgr_withPoints` o su equivalente disponible en
pgRouting 4.

Límites iniciales:

- ancla originada en el path OSRM: máximo 30 m a una arista compatible;
- parada que se intenta conservar: máximo 50 m;
- dirección compatible: diferencia angular máxima de 60°;
- ranking: dirección, distancia, orden de la ruta e `id_calle`.

Los 30 m toleran diferencias de versión y calzadas separadas sin volver a caer
en el problema del vértice más cercano. Los 50 m para paradas son coherentes con
la validación actual del grafo; una parada más lejana sólo puede omitirse si
está dentro del radio de 500 m. Si es obligatoria, la variante es no resoluble.

## 6. Arquitectura propuesta

### 6.1. Dominio

Crear `internal/simulation/detour/` sin dependencias hacia PostgreSQL ni TomTom:

- `service.go`: orquestador del caso de uso;
- `types.go`: `Input`, `Criterion`, `Cut`, `TrafficSnapshot`, `Variant`,
  `UncoveredStop` y errores;
- `policy.go`: radios, tolerancias y límites;
- `selector.go`: objetivos lexicográficos y reconstrucción de paradas;
- `geometry.go`: operaciones puras posibles sobre ruta y zonas;
- pruebas junto a cada archivo.

Interfaces del paquete:

- `Repository`: consulta calles afectadas, asocia segmentos de tráfico al grafo,
  crea puntos virtuales, calcula costos entre puntos y reconstruye geometrías;
- `TrafficProvider`: obtiene y decodifica un snapshot para un conjunto de tiles;
- `Comparator`: reutiliza `simulation.Compare` una vez construida la variante.

El dominio no importa `internal/database/postgres` ni un paquete TomTom.

### 6.2. Infraestructura PostgreSQL

Agregar `internal/database/postgres/detour_repository.go` y SQL embebido en
archivos separados para:

- validar alcance del corte y producir buffers;
- localizar aristas bloqueadas;
- obtener aristas dentro del área de 1 km;
- proyectar anclas y paradas sobre aristas;
- ejecutar pgRouting con puntos virtuales y costos dinámicos;
- reconstruir cada `LineString` en el sentido de circulación;
- unir prefijo original, desvío y sufijo sin saltos ni inversión.

Los costos TomTom son efímeros. Para evitar contaminar `vialis.calles`, se pasan
como datos de la consulta o se cargan en una tabla temporal ligada a una única
conexión adquirida del pool. Si se usa tabla temporal, creación, carga, pgRouting
y lectura deben ejecutarse en la misma conexión/transacción.

Toda consulta debe restringir aristas con `geom && envelope` antes de aplicar
operaciones geográficas más costosas.

### 6.3. Cliente TomTom

Crear `internal/traffic/tomtom/`:

- `client.go`: URL, autenticación, timeout, status y rate limiting;
- `tiles.go`: bbox → lista determinista y deduplicada de tiles;
- `mvt.go`: decodificación de la capa `Traffic flow` a segmentos neutrales del
  proveedor, sin consultar PostgreSQL;
- `cache.go`: caché LRU de 30 minutos y deduplicación concurrente;
- fixtures PBF pequeñas y versionables para pruebas.

La API key es un secreto de despliegue y se incorpora como
`TOMTOM_API_KEY` en `internal/config/config.go`; no puede ser una constante del
modelo. Zoom, TTL, radios, tolerancias de matching y límites sí viven
versionados en `internal/config/parameters.go`.

Agregar una dependencia MVT sólo después de comparar mantenimiento, soporte de
geometría y límites del decoder. El decoder debe rechazar features o tiles que
superen límites antes de reservar memoria sin control.

### 6.4. Composición

En `internal/app/app.go`, agregar `NewDetourService` que reciba el pool y el
cliente de tráfico. `cmd/api/main.go` crea un único cliente/cache por proceso y
lo comparte entre requests.

El proceso puede iniciar sin API key sólo si el endpoint de desvíos queda
explícitamente deshabilitado; una llamada no debe degradar a ruteo por distancia.
La decisión entre fallo de startup o endpoint deshabilitado se cierra junto con
el contrato HTTP.

## 7. Algoritmo por consulta

1. Validar ruta, criterio y único `LineString`.
2. Crear el corredor prohibido de 5 m y el área de búsqueda de 1 km usando
   `geography`, para que las distancias estén en metros.
3. Encontrar la porción de la ruta dentro del área de búsqueda y sus anclas.
4. Clasificar paradas con `ST_DWithin` a 20 m y 500 m, sin materializar buffers
   adicionales.
5. Enumerar y limitar tiles z14 que intersectan el buffer de 1 km.
6. Obtener todos los tiles desde caché/TomTom; abortar ante error.
7. Decodificar y asociar segmentos de tráfico con aristas del subgrafo.
8. Asociar el corte con aristas y retirarlas en ambos costos dirigidos que
   correspondan.
9. Crear puntos virtuales para anclas y paradas candidatas.
10. Calcular costos y caminos entre puntos relevantes con `pgr_withPoints`.
11. Formar un DAG en el orden de la ruta: cada arco representa ir desde un punto
    conservado al siguiente candidato omitiendo los intermedios permitidos.
12. Resolver el objetivo lexicográfico:
    - menor suma de segundos, luego menos omisiones; o
    - menos omisiones, luego menor suma de segundos.
13. Reconstruir la geometría elegida y coserla con las partes originales.
14. Ejecutar `route.Validate` sobre la variante completa. Nunca corregir una
    salida inválida silenciosamente.
15. Llamar a `simulation.Compare` con original y variante.
16. Devolver comparación, paradas omitidas, calles bloqueadas, metadatos del
    grafo, tiempo de decisión y metadata del snapshot TomTom.

El DAG evita enumerar `2^n` subconjuntos y preserva el orden de las paradas. Los
resultados se desempatan de forma estable por costo, cantidad de omisiones,
orden de parada e IDs de arista.

## 8. Errores del dominio

Definir errores tipados antes del handler, aunque el mapeo HTTP se cierre luego:

- `invalid_cut`;
- `cut_outside_graph`;
- `cut_not_matched`;
- `route_not_affected`;
- `anchor_not_matched`;
- `required_stop_not_matched`;
- `no_detour_within_search_area`;
- `traffic_unavailable`;
- `traffic_stale`;
- `traffic_coverage_insufficient`;
- `traffic_tile_limit_exceeded`.

Cada error debe distinguir entrada inválida, ausencia legítima de solución y
fallo de dependencia. No incluir la API key, URL firmada ni cuerpos completos de
TomTom en logs o respuestas.

## 9. Fases de implementación

### Fase 0 — spike de tráfico y matching

1. Obtener una key de TomTom y confirmar cobertura AMBA.
2. Descargar los mismos casos en z14, z15 y z16.
3. Medir tiles, bytes, calles presentes, cobertura de longitud, falsos matches y
   tiempo de procesamiento.
4. Probar al menos: calle simple, avenida dividida, giro, barrera que cruza una
   calle perpendicular, paso a distinto nivel, terminal y tramo del conurbano.
5. Congelar zoom y umbrales únicamente si el matching alcanza los criterios de
   la sección 5.2.

**Estado:** comparación inicial y barrido completo de CABA completados; se
adoptó z14. CABA conserva 58,6 % de longitud total, pero entre 82 % y 100 % de
las principales clases arteriales. Los resultados y el consumo exacto están en
[`spike_tomtom_resultados.md`](spike_tomtom_resultados.md). Aún falta validar
matching dirigido y casos sobre recorridos completos.

**Salida:** informe reproducible y fixtures anonimizadas/permitidas por licencia.
Si TomTom no cubre suficientes calles para formar caminos sin velocidades
inventadas, RF05 queda bloqueado y no se oculta con fallback.

### Fase 1 — dominio y validación

1. Crear tipos, criterios, política y errores.
2. Validar el único corte y clasificar paradas.
3. Implementar comparación lexicográfica y DAG con repositorios falsos.
4. Probar que `MENOR_TIEMPO` y `MENOR_PARADAS_PERDIDAS` divergen en el caso
   esperado.

**Salida:** selector puro probado, todavía sin DB ni HTTP.

### Fase 2 — proveedor TomTom

1. Implementar cobertura de tiles, cliente, decoder, rate limit y timeout.
2. Implementar LRU de 30 minutos y deduplicación concurrente.
3. Añadir métricas de requests, cache hits y antigüedad.

**Salida:** `TrafficProvider` determinista para un snapshot dado.

### Fase 3 — repositorio pgRouting

1. Implementar matching del corte.
2. Asociar los segmentos neutrales de tráfico con `vialis.calles` y medir su
   cobertura.
3. Implementar puntos virtuales en aristas dirigidas.
4. Inyectar costos efímeros y excluir aristas cortadas.
5. Calcular matriz/caminos y reconstruir geometría orientada.
6. Agregar pruebas de integración PostGIS + pgRouting.

**Salida:** caminos locales válidos sin usar el vértice más cercano.

### Fase 4 — servicio completo

1. Integrar buffers, tráfico, repositorio, selector y reconstrucción.
2. Validar siempre la ruta resultante.
3. Reutilizar `simulation.Compare` para evaluación estable.
4. Agregar trazabilidad del grafo y del snapshot.

**Salida:** servicio RF05 invocable desde Go.

### Fase 5 — contrato HTTP

Se difiere hasta acordar el contrato. Después:

1. definir endpoint, request, response y status por error;
2. actualizar `docs/openapi.yaml`;
3. agregar handler e interfaz en `internal/httpapi`;
4. cablear en `cmd/api`;
5. agregar pruebas de handler y ejemplos ejecutables.

### Fase 6 — endurecimiento y demo

1. Ejecutar casos reales con líneas 132A, 60ENIE y cortes conocidos.
2. Verificar que la variante no atraviesa el corte ni sale del radio de 1 km.
3. Comparar ambos criterios y revisar visualmente calles/paradas.
4. Medir latencia fría y caliente, tiles por request y uso mensual proyectado.
5. Documentar ejecución local sin versionar PBF ni API keys.

## 10. Estrategia de pruebas

### Unitarias

- validación GeoJSON y límite de un corte;
- distancia parada–LineString en bordes de 500 m;
- clasificación forzada/opcional/obligatoria;
- tile coverage z14, incluido antimeridiano aunque AMBA no lo cruce;
- cache hit, expiración a 30 minutos y `singleflight`;
- decoder MVT y propiedades ausentes;
- matching por distancia, dirección y desempates;
- objetivos lexicográficos;
- reconstrucción y orientación de geometría;
- cancelación por contexto.

### Integración PostgreSQL

- corte alineado con una arista;
- diferencias laterales dentro/fuera del corredor de 5 m;
- cruce perpendicular bloqueado;
- avenida con calzadas separadas;
- punto virtual en mitad de arista;
- respeto de `oneway` y `reverse_cost`;
- ruta imposible por límite de 1 km;
- parada obligatoria sin snap;
- exclusión de aristas sin costo TomTom;
- geometría final continua y válida.

### Cliente externo

Las pruebas normales no llaman a TomTom. Usan servidor HTTP falso y PBF
fixture. Una prueba manual o suite opt-in, protegida por API key, confirma el
contrato real sin volver frágil `go test ./...`.

### Regresión

`go test ./...`, `go vet ./...`, `go build ./...` y `gofmt -l .` deben seguir
pasando. Simulaciones y comparaciones actuales no cambian cuando RF05 no se usa.

## 11. Observabilidad y seguridad

Registrar por consulta, sin datos secretos:

- criterio;
- longitud del corte;
- cantidad de tiles/hits/misses;
- edad máxima del tráfico;
- aristas candidatas, bloqueadas, con match y sin match;
- cobertura de matching;
- paradas forzadas/opcionales/omitidas;
- duración de cada etapa;
- `calles_metadata.id_carga`.

Exponer métricas agregadas para cuota TomTom y latencia. Limitar tamaño de body,
puntos del LineString, longitud, tiles y concurrencia. Aplicar timeout propio a
TomTom y conservar el deadline general del request.

## 12. Criterios de aceptación del MVP

- Acepta exactamente una ruta y un corte `LineString` válido.
- Trata el `LineString` como barrera y bloquea también las calles
  perpendiculares que la cruzan.
- Nunca modifica geometría fuera del buffer de 1 km.
- Nunca omite una parada a más de 500 m del corte.
- Respeta sentidos de circulación y usa puntos virtuales sobre aristas.
- Todos los costos usados para decidir provienen del snapshot TomTom declarado.
- Si tráfico o matching no alcanzan, devuelve un error explícito.
- Los dos criterios producen resultados deterministas y respetan sus prioridades
  lexicográficas.
- La variante pasa `route.Validate` antes de simularse.
- La comparación final usa sin cambios demanda, tiempo GTFS y recaudación.
- Una segunda consulta sobre la misma zona dentro de 30 minutos reutiliza la
  caché y no descarga nuevamente sus tiles.
- La respuesta identifica paradas perdidas, aristas bloqueadas, carga del grafo,
  zoom y antigüedad del tráfico.

## 13. Decisiones diferidas

- forma exacta del endpoint y status HTTP;
- nombre definitivo de campos JSON;
- fallo de startup o endpoint deshabilitado cuando falta `TOMTOM_API_KEY`;
- ahorro mínimo por parada omitida para suavizar `MENOR_TIEMPO`, sólo si producto
  decide cambiar el criterio;
- múltiples cortes y unión de áreas;
- caché distribuida o persistente;
- revalidación HTTP y TTL menor;
- fallback explícito a distancia como un criterio distinto;
- persistencia y versionado de simulaciones de corte.
