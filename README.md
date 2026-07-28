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

## Ejecutar

```bash
go run ./cmd/api
```

Al iniciar, el proceso crea un pool de conexiones y comprueba que PostgreSQL esté
disponible. Si no puede conectarse, finaliza con error.

## Endpoint

- `GET /health`: responde siempre `200` mientras el servicio esté ejecutándose.

```json
{"status":"ok"}
```

## Probar una simulación

El ejecutable de prueba recibe un archivo JSON con las paradas ordenadas. Cada
parada, salvo la última, contiene un `pathToNext` GeoJSON con el recorrido exacto
hasta la siguiente:

```bash
go run ./cmd/simulation-test -route-file ./examples/simulation_route.json
```

GeoJSON expresa cada coordenada como `[longitud, latitud]`. El primer punto del
`LineString` debe coincidir con la parada actual y el último con la siguiente,
con una tolerancia de 20 metros. La última parada no lleva `pathToNext`.

El resultado separa `demand` de `metrics`. La distancia se mide sobre cada
`LineString` con PostGIS. El tiempo devuelve escenarios `offPeak`, `typical` y
`peak`, estimados con los percentiles 25, 50 y 75 de tramos GTFS cercanos y de
dirección compatible. Si no hay referencias locales, se utiliza la mediana
global y se informa confianza baja.

La conexión también puede reemplazarse con `-database-url`.
