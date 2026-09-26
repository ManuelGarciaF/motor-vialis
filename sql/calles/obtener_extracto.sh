#!/usr/bin/env bash
set -euo pipefail

if (($# != 3)); then
    echo "Uso: $0 <origen.osm.pbf> <poligono.geojson|min_lon,min_lat,max_lon,max_lat> <directorio-salida>" >&2
    exit 2
fi

source_pbf=$1
area=$2
output_dir=$3
allowed_highways='motorway,trunk,primary,secondary,tertiary,unclassified,residential,living_street,motorway_link,trunk_link,primary_link,secondary_link,tertiary_link'

command -v osmium >/dev/null || {
    echo "Falta osmium: instalá osmium-tool y dejalo disponible en PATH" >&2
    exit 1
}
[[ -f "$source_pbf" ]] || { echo "No existe $source_pbf" >&2; exit 1; }
mkdir -p "$output_dir"

if [[ -f "$area" ]]; then
    osmium extract --overwrite --polygon "$area" \
        "$source_pbf" -o "$output_dir/area.osm.pbf"
else
    osmium extract --overwrite --bbox "$area" \
        "$source_pbf" -o "$output_dir/area.osm.pbf"
fi
osmium tags-filter --overwrite "$output_dir/area.osm.pbf" \
    "w/highway=$allowed_highways" -o "$output_dir/ways.osm.pbf"
# Las restricciones de giro se filtran aparte y sin sus miembros: los ways y el
# nodo via que importan ya están en ways.osm.pbf, y traer los referenciados
# agregaría ways de clases excluidas (service, footway) con todos sus nodos.
osmium tags-filter --overwrite --omit-referenced "$output_dir/area.osm.pbf" \
    "r/type=restriction" -o "$output_dir/restricciones.osm.pbf"
osmium merge --overwrite "$output_dir/ways.osm.pbf" \
    "$output_dir/restricciones.osm.pbf" -o "$output_dir/calles.osm.pbf"
rm "$output_dir/ways.osm.pbf" "$output_dir/restricciones.osm.pbf"
osmium cat --overwrite "$output_dir/calles.osm.pbf" \
    -o "$output_dir/calles.osm"
osmium fileinfo -e "$output_dir/calles.osm.pbf" \
    > "$output_dir/calles.fileinfo.txt"
sha256sum "$source_pbf" > "$output_dir/origen.sha256"

printf 'Extracto listo en %s\n' "$output_dir"
