#!/usr/bin/env bash
#
# Crea la base, el esquema, las extensiones y las tablas si todavía no existen,
# y a continuación ejecuta los scripts .sql que reciba como argumento.
#
# Cada rama de ingesta lo ejecuta como primer paso. Es idempotente y barato, de
# modo que ninguna rama depende de que otra se haya corrido antes: no hace
# falta ordenar los grupos de procesos entre sí.
#
# Con --pasar-contenido, todo lo que llega por STDIN se reemite tal cual por
# STDOUT. NiFi necesita eso cuando este paso está delante del archivo a
# ingerir: la relación 'output stream' de ExecuteStreamCommand solo se emite
# cuando el comando termina con código 0, así que reemitir el contenido acá es
# lo que hace que un error corte la cadena en lugar de propagar el archivo.
# Por eso la salida de psql va a STDERR: mezclarla con STDOUT corrompería el
# archivo que sigue viaje.
#
# Uso:
#     preparar_base.sh [--pasar-contenido] [archivo.sql ...]
set -euo pipefail

pasar_contenido=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --pasar-contenido)
            pasar_contenido=true
            shift
            ;;
        --)
            shift
            break
            ;;
        -*)
            echo "preparar_base.sh: opción desconocida '$1'" >&2
            exit 2
            ;;
        *)
            break
            ;;
    esac
done

directorio="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
sql="${VIALIS_SQL_DIR:-/opt/vialis/sql}"
base="${PGDATABASE:-vialis}"
base_admin="${PGDATABASE_ADMIN:-postgres}"

# CREATE DATABASE no puede ejecutarse desde la base que se está creando.
# El lock de aviso va primero en cada sesión: las tres ramas del pipeline
# pueden ejecutar esto a la vez y el DDL concurrente falla sin él.
"${directorio}/ejecutar_sql.sh" \
    --base "${base_admin}" \
    -v "nombre_base=${base}" \
    "${sql}/pipeline/bloquear_preparacion.sql" \
    "${sql}/crear_base.sql" >&2

"${directorio}/ejecutar_sql.sh" \
    --base "${base}" \
    "${sql}/pipeline/bloquear_preparacion.sql" \
    "${sql}/init_db.sql" \
    "${sql}/ddl.sql" >&2

if [[ $# -gt 0 ]]; then
    "${directorio}/ejecutar_sql.sh" --base "${base}" "$@" >&2
fi

echo "base ${base}: esquema y tablas listos" >&2

if [[ "${pasar_contenido}" == true ]]; then
    cat
fi
