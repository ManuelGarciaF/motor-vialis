#!/usr/bin/env bash
set -euo pipefail

if (($# != 1)); then
    echo "Uso: $0 <calles.osm>" >&2
    exit 2
fi

osm_file=$1
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
: "${PGHOST:=localhost}"
: "${PGPORT:=5432}"
: "${PGDATABASE:=vialis}"
: "${PGUSER:=postgres}"
: "${PGPASSWORD:?Definí PGPASSWORD para osm2pgrouting}"
: "${OSM2PGROUTING:=osm2pgrouting}"

command -v "$OSM2PGROUTING" >/dev/null || {
    echo "No se encontró osm2pgrouting: $OSM2PGROUTING" >&2
    exit 1
}
[[ -f "$osm_file" ]] || { echo "No existe $osm_file" >&2; exit 1; }

"$OSM2PGROUTING" \
    --file "$osm_file" \
    --conf "$script_dir/mapconfig.xml" \
    --dbname "$PGDATABASE" \
    --username "$PGUSER" \
    --password "$PGPASSWORD" \
    --host "$PGHOST" \
    --port "$PGPORT" \
    --schema vialis \
    --prefix calles_ \
    --suffix _raw \
    --addnodes \
    --tags \
    --clean
