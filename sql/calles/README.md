# Red vial OpenStreetMap

Este módulo construye el grafo que habilita RF05. La carga es administrada: no
se ejecuta durante una simulación ni al iniciar normalmente la API.

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
ruteo de Vialis no las consulta. Se usa `--addnodes --tags` porque la tabla de
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

Para crear el extracto definitivo:

```bash
nix shell nixpkgs#osmium-tool -c \
  sql/calles/obtener_extracto.sh \
  argentina-260827.osm.pbf \
  sql/calles/amba-margen-10km.geojson \
  /tmp/vialis-calles
```

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
del alcance de la carga. La comparación masiva contra segmentos GTFS se incorporará después
de aprobar el extracto territorial definitivo.

## Decisiones de v1

- Se excluyen `service`, `track` y vías no vehiculares.
- `access=no/private` y `motor_vehicle=no/private` se excluyen, salvo que OSM
  habilite explícitamente `bus` o `psv`.
- `oneway=reversible/alternating` se trata como bidireccional.
- No se procesan restricciones de giro.
- El costo representa distancia, no tiempo.
