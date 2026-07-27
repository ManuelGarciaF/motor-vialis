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

El ejecutable de prueba utiliza la configuración PostgreSQL del proyecto y
simula las 17 paradas provistas para el recorrido de la línea 132:

```bash
go run ./cmd/simulation-test
```

Para indicar otro recorrido, repetir `-stop` respetando el orden:

```bash
go run ./cmd/simulation-test \
  -stop "A,-34.6037,-58.3816" \
  -stop "B,-34.6083,-58.3712" \
  -stop "C,-34.6142,-58.3608"
```

La conexión también puede reemplazarse con `-database-url`.
