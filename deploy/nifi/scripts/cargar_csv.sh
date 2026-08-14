#!/usr/bin/env bash
#
# Carga en una tabla raw el CSV que llega por STDIN.
#
# NiFi entrega el contenido del FlowFile por STDIN y este script lo empuja a
# PostgreSQL con COPY, que es un orden de magnitud más rápido que insertar fila
# por fila con JDBC (stop_times.txt de un feed AMBA tiene millones de filas).
#
# El encabezado se lee acá y define una tabla temporal de staging con todas sus
# columnas como TEXT. `volcar_staging.sql` copia después a la tabla destino
# emparejando **por nombre**, de modo que un feed que agregue o reordene
# columnas no corrompe la carga.
#
# Uso:
#     cargar_csv.sh --tabla vialis.gtfs_stops_raw [--delimitador ,] [--codificacion UTF8]
set -euo pipefail

tabla=""
delimitador=","
codificacion="UTF8"
sql="${VIALIS_SQL_DIR:-/opt/vialis/sql}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --tabla)
            tabla="$2"
            shift 2
            ;;
        --delimitador)
            delimitador="$2"
            shift 2
            ;;
        --codificacion)
            codificacion="$2"
            shift 2
            ;;
        *)
            echo "cargar_csv.sh: opción desconocida '$1'" >&2
            exit 2
            ;;
    esac
done

if [[ -z "${tabla}" ]]; then
    echo "cargar_csv.sh: falta --tabla" >&2
    exit 2
fi

# El nombre de la tabla y los del encabezado terminan interpolados en SQL, así
# que se validan como identificadores antes de usarlos.
if [[ ! "${tabla}" =~ ^[a-zA-Z_][a-zA-Z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*$ ]]; then
    echo "cargar_csv.sh: nombre de tabla inválido '${tabla}'" >&2
    exit 2
fi

if [[ ${#delimitador} -ne 1 ]]; then
    echo "cargar_csv.sh: el delimitador debe ser un único carácter" >&2
    exit 2
fi

if ! IFS= read -r encabezado; then
    echo "cargar_csv.sh: el archivo está vacío" >&2
    exit 2
fi

encabezado="${encabezado%$'\r'}"             # finales de línea CRLF
encabezado="${encabezado#$'\xef\xbb\xbf'}"   # BOM UTF-8

declare -a nombres=()
IFS="${delimitador}" read -r -a nombres <<< "${encabezado}"

definicion=""
for nombre in "${nombres[@]}"; do
    nombre="${nombre//\"/}"
    nombre="${nombre#"${nombre%%[![:space:]]*}"}"   # espacios a la izquierda
    nombre="${nombre%"${nombre##*[![:space:]]}"}"   # espacios a la derecha

    if [[ ! "${nombre}" =~ ^[a-zA-Z_][a-zA-Z0-9_]*$ ]]; then
        echo "cargar_csv.sh: columna inválida en el encabezado: '${nombre}'" >&2
        exit 2
    fi

    # PostgreSQL pliega a minúsculas los identificadores sin comillas: el
    # staging usa la misma forma que las columnas de las tablas raw.
    definicion+="${definicion:+, }${nombre,,} TEXT"
done

# Las cuatro sentencias comparten sesión, que es lo que mantiene viva la tabla
# temporal y el valor de vialis.tabla_destino entre una y otra.
# Sin --quiet a propósito: esa opción también suprime la etiqueta "COPY n" que
# se usa más abajo para informar cuántas filas entraron.
salida="$(psql \
    --no-psqlrc \
    --set ON_ERROR_STOP=1 \
    --command "CREATE TEMP TABLE staging_csv (${definicion})" \
    --command "\\copy staging_csv FROM STDIN WITH (FORMAT csv, DELIMITER '${delimitador}', ENCODING '${codificacion}')" \
    --command "SET vialis.tabla_destino = '${tabla}'" \
    --file "${sql}/pipeline/volcar_staging.sql")"

# La cantidad de filas se toma de la etiqueta que ya devuelve COPY, en lugar de
# volver a contar la tabla: un COUNT(*) sobre las tablas grandes sería un scan
# completo en cada carga.
filas="$(sed -n 's/^COPY \([0-9]\{1,\}\)$/\1/p' <<< "${salida}" | tail -n 1)"

echo "${tabla}: ${filas:-0} filas"
