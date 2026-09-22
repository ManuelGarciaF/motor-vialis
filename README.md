# vialis-motor

Servicio REST en Go para el motor de simulación de Vialis.

## Levantar la base de datos

```bash
docker compose up -d --build
go run ./cmd/initdb
```

El contenedor publica PostgreSQL 18, PostGIS, H3 y pgRouting en `localhost:5433`.
`cmd/initdb` crea el esquema y carga GTFS, viajes, tarifas, combinaciones y la
red vial OSM en el orden definido por `internal/database/bootstrap`. Requiere
`osm2pgrouting` 3.x y `osmium-tool` instalados en el host y disponibles en
`PATH`. `OSM2PGROUTING` permite indicar otra ruta para el importador:

```bash
OSM2PGROUTING=/ruta/a/osm2pgrouting go run ./cmd/initdb
```

| Opción | Predeterminado | Para qué |
| --- | --- | --- |
| `--data-dir` | `.` | Directorio con los datos de entrada. |
| `--reset` | `false` | Borra el esquema `vialis` y reconstruye todo. |
| `DATABASE_URL` | `postgresql://postgres:postgres@localhost:5433/vialis` | Base a inicializar. |

Sin `--reset`, el comando se niega a sobrescribir un esquema existente. Para
usar la API contra el contenedor:

```bash
DATABASE_URL=postgresql://postgres:postgres@localhost:5433/vialis \
TOMTOM_API_KEY=... go run ./cmd/api
```

## Datos de entrada

`--data-dir` debe contener:

- `viajes_BAdata_20241016.csv`;
- `colectivos-gtfs/` con los siete archivos enumerados en
  `sql/recorridos/README.md`;
- `calles.osm`, el extracto vial completo generado según
  `sql/calles/README.md`.

Ningún dataset de entrada se versiona. El CSV de viajes proviene de
[BA Data](https://data.buenosaires.gob.ar/dataset/viajes-etapas-transporte-publico).
El feed se obtiene como un paquete GTFS completo de colectivos —la publicación
original es [Colectivos: GTFS](https://data.buenosaires.gob.ar/dataset/colectivos-gtfs)—
y se extrae en `colectivos-gtfs/`; debe contener `agency.txt`, `routes.txt`,
`trips.txt`, `stops.txt`, `stop_times.txt`, `shapes.txt` y
`calendar_dates.txt`. Si la publicación no ofrece el snapshot utilizado, hay
que obtenerlo por separado: el repositorio no puede reconstruirlo a partir de
los demás datos.

`calles.osm` se genera desde un PBF de OpenStreetMap con
`sql/calles/obtener_extracto.sh`, como explica `sql/calles/README.md`.
`diccionario_viajes.xlsx` no forma parte del pipeline. `cmd/initdb` comprueba
que todos los insumos requeridos existan antes de modificar la base.

## Configuración

La configuración está separada en dos según a quién pertenece cada valor.

### Entorno: lo que define el despliegue

La conexión y dirección HTTP son opcionales. La API exige la credencial TomTom
en el arranque porque expone RF05 junto con los demás endpoints:

| Variable | Predeterminado | Descripción |
| --- | --- | --- |
| `DATABASE_URL` | `postgresql://postgres:postgres@localhost:5432/vialis` | Conexión a PostgreSQL. |
| `HTTP_ADDRESS` | `:8080` | Dirección de escucha del servicio. |
| `TOMTOM_API_KEY` | — | Credencial secreta obligatoria para iniciar la API y consultar Traffic Flow en RF05. |

Sin las dos primeras, el motor corre contra una base local en el puerto 5432.

### Secretos locales para herramientas

El repositorio no administra secretos. Para el spike de tráfico se usa
`TOMTOM_API_KEY` desde el entorno. Hay una plantilla versionada y el archivo
local está ignorado por Git:

```bash
cp .env.example .env
chmod 600 .env
# Editar .env sin compartir su contenido.
```

Go no carga `.env` automáticamente. Hay dos formas simples de exportarlo:

```bash
# Opción sin herramientas adicionales, sólo para la terminal actual.
set -a; source .env; set +a

# Opción cómoda: instalar direnv una vez y habilitar el .envrc versionado.
direnv allow
```

`.gitignore` excluye `.env` y cualquier `.env.*`, salvo `.env.example`. No se
debe imprimir la key en logs, comandos, URLs de diagnóstico ni fixtures.

El ejecutable temporal del spike consulta un radio de 1 km alrededor del
Obelisco en zooms 14, 15 y 16, y escribe un resumen JSON sin incluir la key:

```bash
go run ./cmd/tomtom-spike
```

Se puede cambiar el caso y usar un directorio externo como caché read-through
de respuestas PBF. Si un tile ya existe allí, no vuelve a consultar TomTom:

```bash
go run ./cmd/tomtom-spike \
  -lat -34.6037 -lon -58.3816 -radius 1000 \
  -zooms 14,15,16 -output-dir /tmp/tomtom-spike
```

También puede barrer sólo los tiles que intersectan una jurisdicción del
GeoJSON versionado. El barrido completo de CABA en z14 requiere 68 tiles:

```bash
go run ./cmd/tomtom-spike \
  -area-file sql/calles/amba-jurisdicciones.geojson \
  -area-id 02 -zooms 14 -max-tiles 100 \
  -output-dir /tmp/tomtom-spike
```

Para generar el visor comparativo usando esos tiles cacheados:

```bash
go run ./cmd/tomtom-spike \
  -area-file sql/calles/amba-jurisdicciones.geojson \
  -area-id 02 -zooms 14 -max-tiles 100 \
  -output-dir /tmp/tomtom-spike-tiles \
  -viewer-dir .local/tomtom-caba-viewer

go run ./cmd/tomtom-spike-viewer \
  -dir .local/tomtom-caba-viewer -address :8090
```

Luego se abre `http://localhost:8090`. El mapa superpone TomTom en azul,
`vialis.calles` con match directo en verde, con velocidad vecina estimada en
amarillo y sin ninguna cobertura en rojo. Los GeoJSON y el visor
son generados, pueden ser grandes y quedan bajo `.local/`, ignorado por Git.

### Código: los parámetros del modelo

El resto son parámetros del modelo y viven como constantes en
`internal/config/parameters.go`: radio de acceso, método de accesibilidad,
factor de captación, proporción con tarjeta registrada, política de selección de
recorridos GTFS de referencia, exportación de líneas y timeouts del servidor.

Son constantes a propósito. Cada uno cambia los números que informa el motor, así
que modificarlos es modificar el modelo: corresponde a un commit revisable y
citable, no a la variable de entorno del proceso que se haya iniciado. Un
resultado queda determinado por el commit que lo produjo, sin depender del
entorno en que corrió.

Entre ellos, la exportación de las líneas GTFS almacenadas usa:

- `LinesAlignmentToleranceMeters` (`250`): distancia máxima que se corrige entre
  el extremo de un tramo almacenado y su parada. Es mucho más amplia que los 20 m
  que exige la validación de rutas porque responde otra pregunta: si el corte del
  `shape` es reconociblemente el mismo lugar que la parada, no si quien llama
  mandó geometría coherente. Nunca se aplica a la geometría que envía un cliente.
- `LinesDefaultPageSize` (`50`) y `LinesMaximumPageSize` (`200`): tamaño de
  página de `GET /lines` cuando no se pide uno, y tope de lo que puede pedirse.
  `TestLinesPolicyIsCoherent` verifica que el predeterminado no supere al máximo.
- `SimilarityCorridorToleranceMeters`, `SimilarityMinimumCoverage`,
  `SimilarityDefaultResultCount` y `SimilarityMaximumResultCount`: corredor,
  cobertura mínima y límites de `POST /lines/similar`.

Una sola constante del modelo vive junto al código que la aplica, para que los
paquetes de dominio no dependan de `config`: `endpointToleranceMeters` (20 m, en
`internal/simulation/route`).

`internal/app` es el único lugar donde estos parámetros se combinan con los
repositorios PostgreSQL, así que `cmd/api` y `cmd/simulation-test` no pueden
simular con supuestos distintos.

## Ejecutar

```bash
go run ./cmd/api
```

Al iniciar, el proceso exige `TOMTOM_API_KEY`, crea el cliente/cache de tráfico,
abre un pool de conexiones y comprueba que PostgreSQL esté disponible. Si falta
la key o la base no responde, finaliza con error.

## Endpoints

El contrato completo está en `docs/openapi.yaml`.

- `GET /lines`: lista las líneas GTFS almacenadas, sólo metadata. Acepta
  `search`, `bbox` (`minLon,minLat,maxLon,maxLat`), `limit` y `offset`, y
  devuelve el total de coincidencias junto con la página.
- `GET /lines/{id}`: devuelve una línea con sus paradas y la geometría de cada
  tramo. Su campo `route` tiene la forma que acepta `POST /simulations`, así que
  el cliente puede modificarlo y mandarlo como `proposed` de `POST
  /comparisons`. No incluye `jurisdiction`: GTFS no registra qué autoridad
  tarifaria rige una línea y el motor no la deduce de la geometría, así que la
  agrega quien simula.
- `POST /lines/similar`: busca líneas GTFS que cubren el mismo corredor que
  una ruta dibujada y devuelve ambas coberturas por separado.
- `GET /transfers`: pagina el ranking precalculado de combinaciones de líneas,
  opcionalmente por hora.
- `POST /simulations`: simula una ruta propuesta.
- `POST /comparisons`: simula dos rutas y devuelve la diferencia entre ambas.
- `POST /detours`: recibe `route`, un único `cut` GeoJSON `LineString` y
  `criterion` (`MENOR_TIEMPO` o `MENOR_PARADAS_PERDIDAS`); devuelve la variante,
  sus paradas no cubiertas, la comparación y la trazabilidad de tráfico/grafo.

Las líneas propias de un usuario las persiste otro servicio: acá sólo se leen
las que cargó la ETL de GTFS.

## Probar una simulación

El ejecutable de prueba recibe un archivo JSON con las paradas ordenadas. Cada
parada, salvo la última, contiene un `pathToNext` GeoJSON con el recorrido exacto
hasta la siguiente:

```bash
go run ./cmd/simulation-test -route-file ./examples/linea-132.json
```

La entrada también debe incluir `jurisdiction` con uno de `caba`, `province` o
`national`; selecciona el cuadro tarifario almacenado en
`vialis.tarifas_colectivo`. El archivo
`sql/tarifas/insertar_tarifas_vigentes.sql` carga el cuadro inicial.

GeoJSON expresa cada coordenada como `[longitud, latitud]`. El primer punto del
`LineString` debe coincidir con la parada actual y el último con la siguiente,
con una tolerancia de 20 metros. La última parada no lleva `pathToNext`.

El resultado separa `demand`, `revenue` y `metrics`. La recaudación se calcula
por cada par de paradas a partir de su distancia acumulada, la banda tarifaria,
el factor de captación y la mezcla de tarjetas. La distancia se mide sobre cada
`LineString` con PostGIS. El tiempo devuelve escenarios `offPeak`, `typical` y
`peak`, estimados con los percentiles 25, 50 y 75 de tramos GTFS cercanos y de
dirección compatible. Si no hay referencias locales, se utiliza la mediana
global y se informa confianza baja.

La conexión también puede reemplazarse con `-database-url`.
