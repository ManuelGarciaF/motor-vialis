# Red vial OpenStreetMap

Este módulo construye el grafo que habilita RF05. La carga es administrada: no
se ejecuta durante una simulación ni al iniciar normalmente la API. En una base
nueva, `cmd/initdb` ejecuta la importación y transformación como parte del
pipeline completo; los comandos siguientes también permiten recargarla a mano.

## Requisitos

- PostgreSQL con PostGIS, `hstore` y pgRouting.
- `osmium-tool`.
- `osm2pgrouting` 3.x.
- `psql`.

`osm2pgrouting` crea staging en `vialis`. Las tablas relevantes son:

- `calles_ways_raw`;
- `calles_ways_raw_vertices_pgr`;
- `osm_ways`, necesaria para conservar los tags OSM.

También crea `configuration`, `osm_nodes`, `osm_relations` y
`calles_pointsofinterest_raw`. Son staging o auxiliares del importador; el
ruteo de Vialis no las consulta. En particular, `osm_relations` queda vacía:
osm2pgrouting 3.0 sólo conserva relaciones cuyas etiquetas figuran en
`mapconfig.xml` y, aun así, guarda los miembros sin rol y sin nodos. Por eso las
restricciones de giro se leen aparte con `osmium` (sección 5). Se usa `--addnodes --tags` porque la tabla de
aristas de osm2pgrouting 3.0 no conserva `access`, `motor_vehicle`, `bridge`,
`tunnel` ni `layer`.

## 1. Área y extracto

`amba-jurisdicciones.geojson` contiene CABA y los 40 municipios enumerados por
la definición oficial de AMBA. Sus geometrías administrativas provienen de la
API GeoRef y tienen fuente IGN. `amba-margen-10km.geojson` es la unión de esas
41 jurisdicciones con un buffer geodésico de 10.000 metros.

La geometría resultante:

- es válida y tiene una sola parte;
- cubre aproximadamente 20.860 km²;
- tiene extensión `-59.483779,-35.511510,-57.598768,-33.719536`;
- cubre completamente 2.042 de los 2.066 recorridos GTFS y 43.400 de las
  43.594 paradas existentes;
- excluye servicios externos a la definición oficial, principalmente Junín,
  Mercedes y Navarro.

Fuentes:

- definición: <https://www.argentina.gob.ar/dami/centro/amba>;
- geometrías: <https://apis.datos.gob.ar/georef/api/>.

El extracto conserva los ways de las clases admitidas con sus nodos y, además,
todas las relaciones `type=restriction` del área, sin sus miembros: los ways y
nodos que importan ya están en el extracto, y los que no (por ejemplo un
`highway=service` usado como from) no aportan una arista. `osm2pgrouting` ignora
esas relaciones, así que la red importada es la misma con o sin ellas.

Para crear el extracto definitivo:

```bash
sql/calles/obtener_extracto.sh \
  argentina-260827.osm.pbf \
  sql/calles/amba-margen-10km.geojson \
  /tmp/vialis-calles
cp /tmp/vialis-calles/calles.osm ./calles.osm
```

Con `calles.osm` en `--data-dir`, `cmd/initdb` usa la fecha OSM informada por
`osmium`, calcula su SHA-256 e invoca `osm2pgrouting` y la transformación. El
archivo debe corresponder al alcance canónico versionado; un extracto piloto no
se puede publicar como la red completa.

El segundo argumento también acepta `min_lon,min_lat,max_lon,max_lat` para
pilotos pequeños. El PBF de origen es un insumo externo y no debe incorporarse
al repositorio.

## 2. Importar staging

```bash
export PGHOST=localhost PGPORT=5432 PGDATABASE=vialis PGUSER=postgres
export PGPASSWORD=postgres
export OSM2PGROUTING=/ruta/a/osm2pgrouting
sql/calles/importar_calles_raw.sh /tmp/vialis-calles/calles.osm
```

`--clean` reemplaza únicamente las tablas raw creadas por osm2pgrouting. No
toca `viajes`, recorridos, tarifas ni las tablas finales de calles.

## 3. Transformar y publicar

La transformación vuelve a filtrar clases y restricciones de acceso, normaliza
`oneway=-1` invirtiendo la arista, calcula costos geodésicos en metros, conserva
la componente conexa principal y reemplaza atómicamente las tablas finales.

```bash
ALCANCE_GEOJSON=$(jq -c '.features[0].geometry' \
  sql/calles/amba-margen-10km.geojson)

psql -v ON_ERROR_STOP=1 "$DATABASE_URL" \
  -v fuente_url='archivo local: argentina-260827.osm.pbf' \
  -v fecha_descarga='2026-08-29T00:21:00Z' \
  -v fecha_datos='2026-08-27T20:21:06Z' \
  -v checksum_sha256='3dc6f19e86616134ad65d234c6ca40bc2fbbed7db5a26517eeb8c3b8a21f2772' \
  -v alcance_geojson="$ALCANCE_GEOJSON" \
  -v version_herramienta='3.0.0' \
  -f sql/calles/transformar_calles.sql
```

No se debe publicar un extracto piloto en un entorno compartido como si fuera
la red completa del AMBA.

## 4. Validar

```bash
psql -v ON_ERROR_STOP=1 "$DATABASE_URL" \
  -f sql/calles/validar_calles.sql
```

El script bloquea costos, referencias o geometrías inválidas y reporta tamaño,
proporción descartada y cobertura de paradas por una arista a 50 metros dentro
del alcance de la carga. La comparación masiva contra segmentos GTFS se
incorporará después de aprobar el extracto territorial definitivo.

La carga vigente cubre a esa distancia 43.339 de 43.400 paradas (99,86 %). Las
61 restantes están principalmente dentro de terminales y Ciudad Universitaria,
donde el punto GTFS se aleja del eje vial importado. Se decidió tratarlas como
advertencias y no importar `highway=service` únicamente para hacerlas coincidir:
los cortes de RF05 se declaran sobre calles y normalmente no alcanzarán esos
puntos internos. Un desvío que afecte específicamente uno de esos accesos queda
sujeto a revisión manual.

## 5. Restricciones de giro

`cmd/initdb` corre este paso inmediatamente después de `transformar_calles.sql`,
porque las restricciones referencian las aristas y vértices recién publicados.
La carga manual de las secciones 2 y 3 no lo incluye: el paso 1 necesita el
parser de OPL de `internal/database/bootstrap`.

1. `osmium tags-filter --omit-referenced -f opl calles.osm r/type=restriction`
   lista las relaciones, una por línea;
2. `crear_restricciones_raw.sql` recrea `vialis.calles_restricciones_raw`, donde
   se copian etiquetas y miembros tal como están en OSM;
3. `transformar_restricciones.sql` reemplaza `vialis.calles_restricciones`.

`transformar_calles.sql` vacía `calles_restricciones` en su mismo `TRUNCATE`:
recargar sólo las calles sin volver a correr el paso 3 deja la red sin
restricciones. Una base anterior a esta tabla se recarga con
`cmd/initdb --reset`; no hay script de migración, porque poblarla necesita el
importador de `osmium`.

Cada fila de `calles_restricciones` prohíbe pasar de `id_calle_desde` a
`id_calle_hacia` en `id_vertice_via`, con el `osm_relation_id` de origen. Reglas:

- **Vehículo.** Se usa `restriction:bus` si está. Si no, la relación no aplica
  cuando `except` incluye `bus` o `psv`. Si no, se usa
  `restriction:motor_vehicle` y, por último, `restriction`. Las relaciones que
  sólo restringen otros vehículos (`restriction:hgv`, `restriction:bicycle`) o
  que sólo tienen `restriction:conditional` no aplican: el motor no evalúa
  horarios.
- **Forma.** Se traducen sólo relaciones con exactamente un way `from`, un nodo
  `via` y un way `to`. Las que usan ways como `via` quedan fuera (limitación de
  v1), igual que las que tienen miembros sin rol.
- **Aristas.** La arista desde es la del way `from` que puede llegar al vértice
  via respetando su sentido; la arista hacia, la del way `to` que puede salir de
  él. Si el via quedó fuera de la componente principal, o alguno de los ways no
  tiene esa arista (clase excluida o sentido incompatible), se descarta. Si hay
  más de una candidata, se descarta por ambigua en lugar de adivinar.
- **Tipo.** `no_*` prohíbe el par (desde, hacia). `only_*` prohíbe, desde esa
  arista, todas las otras salidas del vértice via, incluida la vuelta en U. No se
  agregan prohibiciones de vuelta en U que OSM no declare.

Sobre el extracto `argentina-latest` del 2026-09-24 hay 6.092 relaciones; se
traducen 5.606 en 5.915 giros prohibidos. Se descartan 270 con way como via,
144 cuyos ways no llegan o no salen del via, 28 con el via fuera de la red, 27
que no aplican a colectivos y 14 con miembros mal formados; tres `only_*` no
producen filas porque su vértice no tiene otra salida.

`validar_calles.sql` bloquea filas cuyo par no pase por su vértice via y
reporta relaciones importadas, traducidas y giros prohibidos.

El ruteo de desvíos (`internal/database/postgres/find_detour_paths.sql`) usa
`pgr_trsp_withPoints` con las restricciones cuyas dos aristas están en el grafo
local, a costo infinito: un par sin camino legal no tiene camino.

## Decisiones de v1

- Se excluyen `service`, `track` y vías no vehiculares.
- `access=no/private` y `motor_vehicle=no/private` se excluyen, salvo que OSM
  habilite explícitamente `bus` o `psv`.
- `oneway=reversible/alternating` se trata como bidireccional.
- Se procesan restricciones de giro con nodo como via (sección 5); las que
  tienen ways como via no.
- El costo representa distancia, no tiempo.
