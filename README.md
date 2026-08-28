# vialis-motor

Servicio REST en Go para el motor de simulación de Vialis.

## Configuración

La conexión local predeterminada es:

```text
postgresql://postgres:postgres@localhost:5432/vialis
```

Cada componente puede configurarse por separado con `DATABASE_HOST`,
`DATABASE_PORT`, `DATABASE_NAME`, `DATABASE_USER` y `DATABASE_PASSWORD`.
`DATABASE_URL` permite reemplazar la conexión completa y tiene prioridad sobre
las variables individuales.

Opcionalmente, `HTTP_ADDRESS` permite cambiar la dirección de escucha; su valor
predeterminado es `:8080`.

La estrategia de accesibilidad se selecciona con
`SIMULATION_ACCESSIBILITY_METHOD`. Los valores disponibles son:

- `linear` (predeterminado): `1 - distancia / radio`.
- `quadratic`: `(1 - distancia / radio)²`, penaliza más las celdas alejadas.

La recaudación potencial usa estas dos proporciones, ambas entre `0` y `1`:

- `SIMULATION_REVENUE_CAPTURE_FACTOR` (predeterminado `1`): proporción de la demanda potencial que se capta.
- `SIMULATION_REGISTERED_CARD_SHARE` (predeterminado `1`): proporción de viajes con tarjeta registrada.

La exportación de las líneas GTFS almacenadas se ajusta con:

- `LINES_ALIGNMENT_TOLERANCE_METERS` (predeterminado `250`): distancia máxima
  que se corrige entre el extremo de un tramo almacenado y su parada. Es mucho
  más amplia que los 20 m que exige la validación de rutas porque responde otra
  pregunta: si el corte del `shape` es reconociblemente el mismo lugar que la
  parada, no si quien llama mandó geometría coherente. Nunca se aplica a la
  geometría que envía un cliente.
- `LINES_DEFAULT_PAGE_SIZE` (predeterminado `50`) y `LINES_MAXIMUM_PAGE_SIZE`
  (predeterminado `200`): tamaño de página de `GET /lines` cuando no se pide uno
  y tope de lo que puede pedirse. El proceso no arranca si el predeterminado
  supera al máximo.

## Ejecutar

```bash
go run ./cmd/api
```

Al iniciar, el proceso crea un pool de conexiones y comprueba que PostgreSQL esté
disponible. Si no puede conectarse, finaliza con error.

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
- `POST /simulations`: simula una ruta propuesta.
- `POST /comparisons`: simula dos rutas y devuelve la diferencia entre ambas.

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
