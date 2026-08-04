\set ON_ERROR_STOP on

-- Índices creados después de COPY: aceleran la transformación sin penalizar
-- la carga del archivo stop_times.txt, que es el más voluminoso del feed.
CREATE UNIQUE INDEX IF NOT EXISTS idx_gtfs_routes_raw_route_id
ON vialis.gtfs_routes_raw (route_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_gtfs_trips_raw_trip_id
ON vialis.gtfs_trips_raw (trip_id);

CREATE INDEX IF NOT EXISTS idx_gtfs_trips_raw_route_direction
ON vialis.gtfs_trips_raw (route_id, direction_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_gtfs_stops_raw_stop_id
ON vialis.gtfs_stops_raw (stop_id);

CREATE INDEX IF NOT EXISTS idx_gtfs_stop_times_raw_trip_sequence
ON vialis.gtfs_stop_times_raw (trip_id, stop_sequence);

CREATE INDEX IF NOT EXISTS idx_gtfs_shapes_raw_shape_sequence
ON vialis.gtfs_shapes_raw (shape_id, shape_pt_sequence);

ANALYZE vialis.gtfs_routes_raw;
ANALYZE vialis.gtfs_trips_raw;
ANALYZE vialis.gtfs_stops_raw;
ANALYZE vialis.gtfs_stop_times_raw;
ANALYZE vialis.gtfs_shapes_raw;

BEGIN;

-- El modelo final define un recorrido como route_id + direction_id. Abortamos
-- si otro feed rompe esa condición en vez de mezclar geometrías silenciosamente.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM vialis.gtfs_trips_raw
        WHERE direction_id IS NULL
            OR direction_id NOT IN (0, 1)
            OR shape_id IS NULL
            OR shape_id = ''
    ) THEN
        RAISE EXCEPTION
            'El feed contiene direction_id inválido o shape_id vacío';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM vialis.gtfs_trips_raw
        GROUP BY route_id, direction_id
        HAVING COUNT(DISTINCT shape_id) <> 1
    ) THEN
        RAISE EXCEPTION
            'Existe un route_id + direction_id sin un único shape_id';
    END IF;
END
$$;

-- Las horas GTFS se conservan como texto en raw porque pueden superar 24:00:00.
-- Aquí se transforman a segundos para calcular duraciones totales y por tramo.
CREATE TEMP TABLE gtfs_stop_times_seconds ON COMMIT DROP AS
SELECT
    trip_id,
    stop_id,
    stop_sequence,
    split_part(arrival_time, ':', 1)::INTEGER * 3600
        + split_part(arrival_time, ':', 2)::INTEGER * 60
        + split_part(arrival_time, ':', 3)::INTEGER
        AS arrival_seconds,
    split_part(departure_time, ':', 1)::INTEGER * 3600
        + split_part(departure_time, ':', 2)::INTEGER * 60
        + split_part(departure_time, ':', 3)::INTEGER
        AS departure_seconds
FROM vialis.gtfs_stop_times_raw;

CREATE INDEX idx_gtfs_stop_times_seconds_trip_sequence
ON gtfs_stop_times_seconds (trip_id, stop_sequence);

CREATE TEMP TABLE gtfs_trip_stats ON COMMIT DROP AS
SELECT
    t.route_id,
    t.direction_id,
    t.shape_id,
    t.trip_id,
    t.trip_headsign,
    COUNT(*)::INTEGER AS cantidad_paradas,
    ARRAY_AGG(st.stop_id ORDER BY st.stop_sequence) AS stop_ids,
    GREATEST(
        MAX(st.arrival_seconds) - MIN(st.departure_seconds),
        0
    )::INTEGER AS duracion_segundos
FROM vialis.gtfs_trips_raw t
JOIN gtfs_stop_times_seconds st
    ON st.trip_id = t.trip_id
GROUP BY
    t.route_id,
    t.direction_id,
    t.shape_id,
    t.trip_id,
    t.trip_headsign;

CREATE INDEX idx_gtfs_trip_stats_route_direction
ON gtfs_trip_stats (route_id, direction_id);

-- Si hubiera servicios parciales para el mismo ramal/direction_id, el recorrido
-- canónico será el viaje con más paradas y luego el de mayor duración.
CREATE TEMP TABLE gtfs_canonical_trips ON COMMIT DROP AS
SELECT
    route_id,
    direction_id,
    shape_id,
    trip_id,
    trip_headsign,
    stop_ids
FROM (
    SELECT
        ts.*,
        ROW_NUMBER() OVER (
            PARTITION BY ts.route_id, ts.direction_id
            ORDER BY
                ts.cantidad_paradas DESC,
                ts.duracion_segundos DESC,
                ts.trip_id
        ) AS prioridad
    FROM gtfs_trip_stats ts
) ranked
WHERE prioridad = 1;

CREATE UNIQUE INDEX idx_gtfs_canonical_trips_route_direction
ON gtfs_canonical_trips (route_id, direction_id);

CREATE TEMP TABLE gtfs_route_durations ON COMMIT DROP AS
SELECT
    route_id,
    direction_id,
    ROUND(
        percentile_cont(0.5) WITHIN GROUP (ORDER BY duracion_segundos)
        / 60.0
    )::SMALLINT AS tiempo_total_minutos
FROM gtfs_trip_stats
GROUP BY route_id, direction_id;

-- Para los tiempos por tramo solo se usan viajes con exactamente la misma
-- secuencia ordenada de paradas que el viaje canónico. El intervalo termina en
-- la salida de la próxima parada para incluir detenciones intermedias; en el
-- último tramo termina en la llegada final.
CREATE TEMP TABLE gtfs_route_segment_times ON COMMIT DROP AS
WITH eligible_trip_stops AS (
    SELECT
        stats.route_id,
        stats.direction_id,
        stats.trip_id,
        ROW_NUMBER() OVER (
            PARTITION BY stats.trip_id
            ORDER BY stop_time.stop_sequence
        )::INTEGER AS stop_ordinal,
        COUNT(*) OVER (
            PARTITION BY stats.trip_id
        )::INTEGER AS stop_count,
        stop_time.departure_seconds,
        LEAD(stop_time.arrival_seconds) OVER (
            PARTITION BY stats.trip_id
            ORDER BY stop_time.stop_sequence
        ) AS next_arrival_seconds,
        LEAD(stop_time.departure_seconds) OVER (
            PARTITION BY stats.trip_id
            ORDER BY stop_time.stop_sequence
        ) AS next_departure_seconds
    FROM gtfs_trip_stats stats
    JOIN gtfs_canonical_trips canonical
        ON canonical.route_id = stats.route_id
        AND canonical.direction_id = stats.direction_id
        AND canonical.stop_ids = stats.stop_ids
    JOIN gtfs_stop_times_seconds stop_time
        ON stop_time.trip_id = stats.trip_id
), segment_samples AS (
    SELECT
        route_id,
        direction_id,
        stop_ordinal,
        CASE
            WHEN stop_ordinal = stop_count - 1
            THEN next_arrival_seconds
            ELSE next_departure_seconds
        END - departure_seconds AS duration_seconds
    FROM eligible_trip_stops
    WHERE next_arrival_seconds IS NOT NULL
)
SELECT
    route_id,
    direction_id,
    stop_ordinal,
    ROUND(
        percentile_cont(0.25) WITHIN GROUP (ORDER BY duration_seconds)
    )::INTEGER AS tiempo_valle_segundos,
    ROUND(
        percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_seconds)
    )::INTEGER AS tiempo_tipico_segundos,
    ROUND(
        percentile_cont(0.75) WITHIN GROUP (ORDER BY duration_seconds)
    )::INTEGER AS tiempo_pico_segundos,
    COUNT(*)::INTEGER AS cantidad_muestras
FROM segment_samples
WHERE duration_seconds > 0
GROUP BY route_id, direction_id, stop_ordinal;

CREATE UNIQUE INDEX idx_gtfs_route_segment_times_route_direction_stop
ON gtfs_route_segment_times (route_id, direction_id, stop_ordinal);

CREATE TEMP TABLE gtfs_shape_geometries ON COMMIT DROP AS
SELECT
    shape_id,
    geom::GEOMETRY(LineString, 4326) AS geom,
    ROUND(ST_Length(geom::geography))::INTEGER AS distancia_metros
FROM (
    SELECT
        shape_id,
        ST_MakeLine(
            ST_SetSRID(ST_MakePoint(shape_pt_lon, shape_pt_lat), 4326)
            ORDER BY shape_pt_sequence
        ) AS geom
    FROM vialis.gtfs_shapes_raw
    GROUP BY shape_id
) shapes;

CREATE UNIQUE INDEX idx_gtfs_shape_geometries_shape_id
ON gtfs_shape_geometries (shape_id);

TRUNCATE TABLE
    vialis.recorridos_paradas,
    vialis.recorridos,
    vialis.paradas
RESTART IDENTITY;

INSERT INTO vialis.recorridos (
    gtfs_route_id,
    gtfs_shape_id,
    nombre_publico,
    linea,
    ramal,
    direction_id,
    destino,
    descripcion,
    geom,
    distancia_metros,
    tiempo_total_minutos
)
SELECT
    ct.route_id,
    ct.shape_id,
    r.route_short_name,
    COALESCE(
        substring(r.route_short_name FROM '^([[:digit:]]+|[[:alpha:]]+)'),
        r.route_short_name
    ) AS linea,
    COALESCE(
        NULLIF(
            regexp_replace(
                r.route_short_name,
                '^([[:digit:]]+|[[:alpha:]]+)',
                ''
            ),
            ''
        ),
        'TRONCAL'
    ) AS ramal,
    ct.direction_id,
    NULLIF(ct.trip_headsign, ''),
    NULLIF(r.route_desc, ''),
    sg.geom,
    sg.distancia_metros,
    durations.tiempo_total_minutos
FROM gtfs_canonical_trips ct
JOIN vialis.gtfs_routes_raw r
    ON r.route_id = ct.route_id
JOIN gtfs_shape_geometries sg
    ON sg.shape_id = ct.shape_id
JOIN gtfs_route_durations durations
    ON durations.route_id = ct.route_id
    AND durations.direction_id = ct.direction_id;

INSERT INTO vialis.paradas (
    gtfs_stop_id,
    codigo,
    nombre,
    posicion
)
SELECT DISTINCT
    s.stop_id,
    NULLIF(s.stop_code, ''),
    s.stop_name,
    ST_SetSRID(ST_MakePoint(s.stop_lon, s.stop_lat), 4326)
        ::GEOMETRY(Point, 4326)
FROM gtfs_canonical_trips ct
JOIN vialis.gtfs_stop_times_raw st
    ON st.trip_id = ct.trip_id
JOIN vialis.gtfs_stops_raw s
    ON s.stop_id = st.stop_id;

WITH stop_fractions AS (
    SELECT
        recorrido.id_recorrido,
        ct.route_id,
        ct.direction_id,
        parada.id_parada,
        st.stop_sequence AS nro_parada,
        ROW_NUMBER() OVER (
            PARTITION BY recorrido.id_recorrido
            ORDER BY st.stop_sequence
        )::INTEGER AS stop_ordinal,
        recorrido.geom,
        ST_LineLocatePoint(recorrido.geom, parada.posicion) AS fraccion
    FROM gtfs_canonical_trips ct
    JOIN vialis.recorridos recorrido
        ON recorrido.gtfs_route_id = ct.route_id
        AND recorrido.direction_id = ct.direction_id
    JOIN gtfs_shape_geometries sg
        ON sg.shape_id = ct.shape_id
    JOIN vialis.gtfs_stop_times_raw st
        ON st.trip_id = ct.trip_id
    JOIN vialis.paradas parada
        ON parada.gtfs_stop_id = st.stop_id
), ordered_stops AS (
    SELECT
        id_recorrido,
        route_id,
        direction_id,
        id_parada,
        nro_parada,
        stop_ordinal,
        geom,
        fraccion,
        LEAD(fraccion) OVER (
            PARTITION BY id_recorrido
            ORDER BY nro_parada
        ) AS fraccion_siguiente
    FROM stop_fractions
), stop_segments AS (
    SELECT
        id_recorrido,
        route_id,
        direction_id,
        id_parada,
        nro_parada,
        stop_ordinal,
        CASE
            WHEN fraccion_siguiente > fraccion
            THEN ST_LineSubstring(geom, fraccion, fraccion_siguiente)
                ::GEOMETRY(LineString, 4326)
        END AS tramo_hasta_siguiente
    FROM ordered_stops
)
INSERT INTO vialis.recorridos_paradas (
    id_recorrido,
    id_parada,
    nro_parada,
    tramo_hasta_siguiente,
    distancia_hasta_siguiente_metros,
    tiempo_valle_hasta_siguiente_segundos,
    tiempo_tipico_hasta_siguiente_segundos,
    tiempo_pico_hasta_siguiente_segundos,
    cantidad_muestras_tiempo
)
SELECT
    segment.id_recorrido,
    segment.id_parada,
    segment.nro_parada,
    segment.tramo_hasta_siguiente,
    CASE
        WHEN segment.tramo_hasta_siguiente IS NOT NULL
        THEN ROUND(
            ST_Length(segment.tramo_hasta_siguiente::geography)
        )::INTEGER
    END,
    times.tiempo_valle_segundos,
    times.tiempo_tipico_segundos,
    times.tiempo_pico_segundos,
    COALESCE(times.cantidad_muestras, 0)
FROM stop_segments segment
LEFT JOIN gtfs_route_segment_times times
    ON times.route_id = segment.route_id
    AND times.direction_id = segment.direction_id
    AND times.stop_ordinal = segment.stop_ordinal;

COMMIT;

VACUUM ANALYZE vialis.recorridos;
VACUUM ANALYZE vialis.paradas;
VACUUM ANALYZE vialis.recorridos_paradas;

SELECT
    (SELECT COUNT(*) FROM vialis.recorridos) AS recorridos,
    (SELECT COUNT(*) FROM vialis.paradas) AS paradas,
    (SELECT COUNT(*) FROM vialis.recorridos_paradas) AS paradas_en_recorridos;
