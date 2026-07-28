WITH valid_segments AS (
    SELECT
        route_stop.id_recorrido,
        route_stop.distancia_hasta_siguiente_metros::DOUBLE PRECISION
            AS distance_meters,
        route_stop.tiempo_valle_hasta_siguiente_segundos::DOUBLE PRECISION
            AS off_peak_seconds,
        route_stop.tiempo_tipico_hasta_siguiente_segundos::DOUBLE PRECISION
            AS typical_seconds,
        route_stop.tiempo_pico_hasta_siguiente_segundos::DOUBLE PRECISION
            AS peak_seconds
    FROM vialis.recorridos_paradas route_stop
    WHERE route_stop.distancia_hasta_siguiente_metros > 0
        AND route_stop.tiempo_valle_hasta_siguiente_segundos > 0
        AND route_stop.tiempo_tipico_hasta_siguiente_segundos > 0
        AND route_stop.tiempo_pico_hasta_siguiente_segundos > 0
        AND (
            route_stop.distancia_hasta_siguiente_metros::DOUBLE PRECISION
            / route_stop.tiempo_tipico_hasta_siguiente_segundos
            * 3.6
        ) BETWEEN $1::DOUBLE PRECISION AND $2::DOUBLE PRECISION
), route_paces AS (
    SELECT
        id_recorrido,
        SUM(off_peak_seconds) / SUM(distance_meters)
            AS off_peak_seconds_per_meter,
        SUM(typical_seconds) / SUM(distance_meters)
            AS typical_seconds_per_meter,
        SUM(peak_seconds) / SUM(distance_meters)
            AS peak_seconds_per_meter
    FROM valid_segments
    GROUP BY id_recorrido
)
SELECT
    percentile_cont(0.5) WITHIN GROUP (
        ORDER BY off_peak_seconds_per_meter
    ) AS off_peak_seconds_per_meter,
    percentile_cont(0.5) WITHIN GROUP (
        ORDER BY typical_seconds_per_meter
    ) AS typical_seconds_per_meter,
    percentile_cont(0.5) WITHIN GROUP (
        ORDER BY peak_seconds_per_meter
    ) AS peak_seconds_per_meter,
    COUNT(*)::INTEGER AS route_count
FROM route_paces;
