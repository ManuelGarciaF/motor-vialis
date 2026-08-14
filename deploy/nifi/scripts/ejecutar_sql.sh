#!/usr/bin/env bash
#
# Ejecuta uno o más scripts .sql en una única sesión de psql.
#
# Los scripts de transformación de Vialis no se pueden partir en sentencias
# sueltas: usan tablas temporales ON COMMIT DROP dentro de una transacción
# explícita y VACUUM fuera de ella. Por eso el pipeline los ejecuta con psql y
# no sentencia por sentencia desde NiFi.
#
# Uso:
#     ejecutar_sql.sh [--base BASE] [-v nombre=valor]... archivo.sql [archivo.sql]...
set -euo pipefail

base="${PGDATABASE:-vialis}"
declare -a variables=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --base)
            base="$2"
            shift 2
            ;;
        -v|--variable)
            variables+=(--set "$2")
            shift 2
            ;;
        --)
            shift
            break
            ;;
        -*)
            echo "ejecutar_sql.sh: opción desconocida '$1'" >&2
            exit 2
            ;;
        *)
            break
            ;;
    esac
done

if [[ $# -eq 0 ]]; then
    echo "ejecutar_sql.sh: falta al menos un archivo .sql" >&2
    exit 2
fi

declare -a archivos=()
for archivo in "$@"; do
    if [[ ! -r "$archivo" ]]; then
        echo "ejecutar_sql.sh: no se puede leer '$archivo'" >&2
        exit 2
    fi
    archivos+=(--file "$archivo")
done

# STDIN se cierra para que un \copy FROM STDIN olvidado en un script no se
# quede leyendo el contenido del FlowFile.
exec psql \
    --dbname "$base" \
    --no-psqlrc \
    --set ON_ERROR_STOP=1 \
    "${variables[@]}" \
    "${archivos[@]}" \
    < /dev/null
