# vialis-motor

Servicio REST en Go para el motor de simulación de Vialis.

## Configuración

La configuración está separada en dos según a quién pertenece cada valor.

### Entorno: lo que define el despliegue

Solo dos variables, ambas opcionales:

| Variable | Predeterminado | Descripción |
| --- | --- | --- |
| `DATABASE_URL` | `postgresql://postgres:postgres@localhost:5432/vialis` | Conexión a PostgreSQL. |
| `HTTP_ADDRESS` | `:8080` | Dirección de escucha del servicio. |

Sin ninguna de las dos, el motor corre contra la base local que crea
`sql/init_db.sql`.

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

La búsqueda de corredores de `POST /lines/similar` agrega otros cuatro:

- `SimilarityCorridorToleranceMeters` (`200`): a qué distancia puede correr una
  línea almacenada de la ruta dibujada y seguir contando como el mismo corredor.
  Es aproximadamente una cuadra de la trama del AMBA: una línea que baja por la
  calle paralela sigue siendo el mismo corredor para quien viaja, porque camina
  hasta la esquina, mientras que una a dos cuadras ya es otro servicio. Está
  cerca de `LinesAlignmentToleranceMeters` (`250`) por coincidencia y no por
  parentesco: aquella decide si el extremo de un tramo almacenado es el mismo
  *lugar* que su parada, y ésta si dos líneas enteras sirven el mismo
  *corredor*. Mover una por lo que le pase a la otra cambiaría una pregunta que
  nadie quiso hacer.
- `SimilarityMinimumCoverage` (`0.20`): qué proporción de una de las dos líneas
  tiene que caer dentro del corredor de la otra para que el par se informe.
  Por debajo de eso sólo se tocan: cualquier línea que cruza una avenida junta
  unos puntos de solape, y devolverlas taparía las pocas que efectivamente
  acompañan la propuesta.
- `SimilarityDefaultResultCount` (`10`) y `SimilarityMaximumResultCount` (`50`):
  cuántas coincidencias devuelve la búsqueda cuando no se pide un número, y tope
  de lo que puede pedirse. Son mucho más chicos que los tamaños de página de
  arriba porque el resultado es una lista corta para elegir una baseline, no un
  listado que se recorre. `TestSimilarityPolicyIsCoherent` verifica que el
  predeterminado entre en el máximo y que la cobertura mínima quede en `(0, 1]`.

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
- `POST /lines/similar`: dada una ruta dibujada, devuelve las líneas GTFS
  almacenadas que corren por el mismo corredor, ordenadas y sin simular nada.
  La similitud es solape geométrico medido como cobertura mutua de corredores:
  se informa qué parte de la ruta dibujada corre dentro del corredor de la línea
  almacenada y qué parte de la línea almacenada corre dentro del de la ruta.
  Los dos números van siempre por separado, porque `1.00 / 0.07` significa "lo
  que dibujaste es un fragmento de esa línea" y promediarlos borraría justamente
  esa distinción. El orden va por la menor de las dos coberturas: una candidata
  sólo se parece de verdad cuando se parece en ambos sentidos. La ruta no lleva
  `jurisdiction` —no se calcula ninguna tarifa— y cada parada puede omitir su
  `pathToNext`, en cuyo caso se toma el segmento recto hasta la siguiente.
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
