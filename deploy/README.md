# Entorno del pipeline de ingesta

PostgreSQL con PostGIS y h3, más Apache NiFi 1.28 con el flow que crea la base
y carga los datos. El diseño y las decisiones están en
[`docs/pipeline_nifi.md`](../docs/pipeline_nifi.md).

## Arranque

```bash
cd deploy
cp .env.example .env        # opcional: todos los valores tienen default
docker compose up -d --build
./nifi/importar_flow.sh
```

- NiFi: <http://localhost:8080/nifi>
- PostgreSQL: `postgresql://postgres:postgres@localhost:5432/vialis`

El puerto 5432 puede chocar con un PostgreSQL que ya esté corriendo. En ese
caso, `PUERTO_POSTGRES` en `.env` lo cambia.

La base `vialis` no existe hasta que corre el pipeline: crearla es su primer
paso.

## Uso

Los procesadores se importan detenidos. Desde la interfaz:

1. **`10 - Preparar base y tarifas`**: arrancar el grupo, o usar *Ejecutar una
   vez* sobre `Disparador manual`. Crea el esquema y carga el cuadro tarifario.
2. **`20 - Ingesta GTFS`**: dejar el ZIP del feed en `datos/entrada/gtfs/` y
   arrancar el grupo. Para bajarlo por HTTP hay que poner la URL real en el
   parámetro `gtfs.url` y habilitar el disparador de descarga.
3. **`30 - Ingesta de viajes`**: dejar el CSV (o un ZIP que lo contenga) en
   `datos/entrada/viajes/` y arrancar el grupo.

Para una prueba rápida con los datos sintéticos del repositorio:

```bash
zip -j datos/entrada/gtfs/feed-ejemplo.zip ../examples/pipeline/gtfs/*.txt
cp ../examples/pipeline/viajes/viajes.csv datos/entrada/viajes/
```

## Contenido

| Ruta                  | Qué es |
|-----------------------|--------|
| `docker-compose.yml`  | PostgreSQL y NiFi |
| `nifi/Dockerfile`     | NiFi 1.28 más el cliente de PostgreSQL |
| `nifi/flow/`          | Flow definition versionado |
| `nifi/importar_flow.sh` | Sube el flow a un NiFi que ya esté corriendo |
| `nifi/scripts/`       | Scripts que ejecuta `ExecuteStreamCommand` |
| `datos/entrada/`      | Carpetas vigiladas por `ListFile` (su contenido no se versiona) |

`nifi/scripts/` se monta de solo lectura dentro del contenedor, igual que
`sql/`: editar un script en el repositorio alcanza para que NiFi use la versión
nueva en la siguiente ejecución, sin reconstruir la imagen.

## Reaplicar el flow

`importar_flow.sh` reemplaza el grupo existente. Si quedaron FlowFiles
encolados se detiene y avisa; `--vaciar-colas` los descarta y sigue.

Los cambios hechos a mano en la interfaz **no** se escriben solos en
`nifi/flow/vialis-ingesta.json`: para versionarlos hay que descargar el flow
definition del grupo desde NiFi y reemplazar el archivo.
