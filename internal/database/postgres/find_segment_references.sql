WITH input_segments AS (
    SELECT
        input.segment_order,
        input.origin_stop_id,
        input.destination_stop_id,
        ST_SetSRID(
            ST_GeomFromGeoJSON(input.path_geojson),
            4326
        )::GEOMETRY(LineString, 4326) AS geom
    FROM unnest(
        $1::INTEGER[],
        $2::TEXT[],
        $3::TEXT[],
        $4::TEXT[]
    ) AS input(
        segment_order,
        origin_stop_id,
        destination_stop_id,
        path_geojson
    )
), measured_segments AS (
    SELECT
        input.*,
        ST_Length(input.geom::geography) AS length_meters,
        ST_Azimuth(
            ST_StartPoint(input.geom),
            ST_EndPoint(input.geom)
        ) AS azimuth
    FROM input_segments input
)
SELECT
    input.segment_order,
    input.origin_stop_id,
    input.destination_stop_id,
    input.length_meters,
    radius.radius_meters,
    reference.id_recorrido,
    reference.distance_meters,
    reference.overlap_meters,
    reference.off_peak_seconds_per_meter,
    reference.typical_seconds_per_meter,
    reference.peak_seconds_per_meter
FROM measured_segments input
CROSS JOIN unnest($5::DOUBLE PRECISION[]) AS radius(radius_meters)
LEFT JOIN LATERAL (
    SELECT
        route_stop.id_recorrido,
        ST_Distance(
            input.geom::geography,
            route_stop.tramo_hasta_siguiente::geography
        ) AS distance_meters,
        ST_Length(
            ST_CollectionExtract(
                ST_Intersection(
                    input.geom,
                    ST_Buffer(
                        route_stop.tramo_hasta_siguiente::geography,
                        radius.radius_meters
                    )::geometry
                ),
                2
            )::geography
        ) AS overlap_meters,
        route_stop.tiempo_valle_hasta_siguiente_segundos::DOUBLE PRECISION
            / route_stop.distancia_hasta_siguiente_metros
            AS off_peak_seconds_per_meter,
        route_stop.tiempo_tipico_hasta_siguiente_segundos::DOUBLE PRECISION
            / route_stop.distancia_hasta_siguiente_metros
            AS typical_seconds_per_meter,
        route_stop.tiempo_pico_hasta_siguiente_segundos::DOUBLE PRECISION
            / route_stop.distancia_hasta_siguiente_metros
            AS peak_seconds_per_meter
    FROM vialis.recorridos_paradas route_stop
    WHERE route_stop.tramo_hasta_siguiente IS NOT NULL
        AND route_stop.distancia_hasta_siguiente_metros > 0
        AND route_stop.tiempo_valle_hasta_siguiente_segundos > 0
        AND route_stop.tiempo_tipico_hasta_siguiente_segundos > 0
        AND route_stop.tiempo_pico_hasta_siguiente_segundos > 0
        AND (
            route_stop.distancia_hasta_siguiente_metros::DOUBLE PRECISION
            / route_stop.tiempo_tipico_hasta_siguiente_segundos
            * 3.6
        ) BETWEEN $7::DOUBLE PRECISION AND $8::DOUBLE PRECISION
        AND ST_DWithin(
            input.geom::geography,
            route_stop.tramo_hasta_siguiente::geography,
            radius.radius_meters
        )
        AND LEAST(
            ABS(
                DEGREES(
                    input.azimuth
                    - ST_Azimuth(
                        ST_StartPoint(route_stop.tramo_hasta_siguiente),
                        ST_EndPoint(route_stop.tramo_hasta_siguiente)
                    )
                )
            ),
            360 - ABS(
                DEGREES(
                    input.azimuth
                    - ST_Azimuth(
                        ST_StartPoint(route_stop.tramo_hasta_siguiente),
                        ST_EndPoint(route_stop.tramo_hasta_siguiente)
                    )
                )
            )
        ) <= $6::DOUBLE PRECISION
) reference ON TRUE
ORDER BY
    input.segment_order,
    radius.radius_meters,
    reference.id_recorrido,
    reference.distance_meters;
