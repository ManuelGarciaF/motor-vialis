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
# Carriles exclusivos de bus (Metrobús): en OSM son ways aparte con
# highway=service|busway|bus_guideway y bus/psv=designated|yes. Se traen porque
# son por donde circula el colectivo —y donde están sus andenes centrales—, que
# si no quedan a ~10 m de las calzadas de autos y el ruteo cruza de una a otra.
# Encadenar dos tags-filter es un AND: primero la clase de vía de bus, después
# que efectivamente habilite bus/psv, así NO entra el resto de los `service`
# (estacionamientos, drive-throughs).
osmium tags-filter --overwrite "$output_dir/area.osm.pbf" \
    "w/highway=service,busway,bus_guideway" -o "$output_dir/vias-bus.osm.pbf"
osmium tags-filter --overwrite "$output_dir/vias-bus.osm.pbf" \
    "w/bus=yes,designated" "w/psv=yes,designated" -o "$output_dir/metrobus.osm.pbf"
# Las restricciones de giro se filtran aparte y sin sus miembros: los ways y el
# nodo via que importan ya están en ways.osm.pbf, y traer los referenciados
# agregaría ways de clases excluidas (footway, service no-bus) con todos sus nodos.
osmium tags-filter --overwrite --omit-referenced "$output_dir/area.osm.pbf" \
    "r/type=restriction" -o "$output_dir/restricciones.osm.pbf"
osmium merge --overwrite "$output_dir/ways.osm.pbf" "$output_dir/metrobus.osm.pbf" \
    "$output_dir/restricciones.osm.pbf" -o "$output_dir/calles.osm.pbf"
rm "$output_dir/ways.osm.pbf" "$output_dir/vias-bus.osm.pbf" \
    "$output_dir/metrobus.osm.pbf" "$output_dir/restricciones.osm.pbf"
osmium cat --overwrite "$output_dir/calles.osm.pbf" \
    -o "$output_dir/calles.osm"
osmium fileinfo -e "$output_dir/calles.osm.pbf" \
    > "$output_dir/calles.fileinfo.txt"
sha256sum "$source_pbf" > "$output_dir/origen.sha256"

printf 'Extracto listo en %s\n' "$output_dir"
