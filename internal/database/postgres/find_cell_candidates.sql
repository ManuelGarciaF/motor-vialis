WITH input_stops AS (
    SELECT
        (input.ordinality - 1)::INTEGER AS stop_order,
        input.stop_id,
        ST_SetSRID(
            ST_MakePoint(input.longitude, input.latitude),
            4326
        ) AS position
    FROM unnest(
        $1::TEXT[],
        $2::DOUBLE PRECISION[],
        $3::DOUBLE PRECISION[]
    ) WITH ORDINALITY AS input(stop_id, longitude, latitude, ordinality)
), candidate_distances AS (
    SELECT
        stop.stop_order,
        stop.stop_id,
        hexagon.indice_h3::TEXT AS cell_id,
        ST_Distance(
            stop.position::geography,
            hexagon.punto_maxima_concurrencia::geography
        ) AS distance_meters
    FROM input_stops stop
    CROSS JOIN LATERAL h3_grid_disk(
        h3_lat_lng_to_cell(stop.position, 8),
        1
    ) AS nearby(cell)
    JOIN vialis.hexagonos_viajes hexagon
        ON hexagon.indice_h3 = nearby.cell
    WHERE hexagon.punto_maxima_concurrencia IS NOT NULL
)
SELECT
    stop_order,
    stop_id,
    cell_id,
    distance_meters
FROM candidate_distances
WHERE distance_meters < $4::DOUBLE PRECISION
ORDER BY stop_order, cell_id;
