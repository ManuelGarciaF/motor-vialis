CREATE TEMP TABLE detour_context ON COMMIT DROP AS
SELECT
    ST_SetSRID(ST_GeomFromGeoJSON($1::jsonb), 4326) AS search_area,
    ST_SetSRID(ST_GeomFromGeoJSON($2::jsonb), 4326) AS forbidden_area;

CREATE TEMP TABLE detour_traffic ON COMMIT DROP AS
SELECT
    ordinality::bigint AS id,
    ST_SetSRID(
        ST_GeomFromGeoJSON(feature -> 'geometry'),
        4326
    )::geometry(LineString, 4326) AS geom,
    COALESCE(feature -> 'properties' ->> 'source_id', ordinality::text) AS source_id,
    COALESCE(feature -> 'properties' ->> 'road_category', '') AS road_category,
    COALESCE(feature -> 'properties' ->> 'road_coverage', '') AS road_coverage,
    COALESCE((feature -> 'properties' ->> 'speed_kph')::double precision, 0) AS speed_kph,
    COALESCE((feature -> 'properties' ->> 'has_speed')::boolean, false) AS has_speed,
    COALESCE((feature -> 'properties' ->> 'closure')::boolean, false) AS closure
FROM jsonb_array_elements($4::jsonb -> 'features')
     WITH ORDINALITY AS item(feature, ordinality);

CREATE INDEX detour_traffic_geom_idx ON detour_traffic USING GIST (geom);

CREATE TEMP TABLE detour_graph ON COMMIT DROP AS
WITH candidate_edges AS (
    SELECT
        street.id_calle AS id,
        CASE
            WHEN ST_Covers(context.search_area, ST_StartPoint(street.geom))
            THEN street.origen
            ELSE 8000000000000000::bigint + street.id_calle * 2
        END AS source,
        CASE
            WHEN ST_Covers(context.search_area, ST_EndPoint(street.geom))
            THEN street.destino
            ELSE 8000000000000001::bigint + street.id_calle * 2
        END AS target,
        street.origen AS original_source,
        street.destino AS original_target,
        street.tipo,
        street.costo AS original_cost,
        street.costo_inverso AS original_reverse_cost,
        street.costo_inverso > 0 AS reverse_allowed,
        ST_Length(street.geom::geography) AS length_meters,
        street.geom,
        CASE
            WHEN street.tipo IN ('motorway', 'motorway_link') THEN 'motorway'
            WHEN street.tipo IN ('trunk', 'trunk_link') THEN 'trunk'
            WHEN street.tipo IN ('primary', 'primary_link') THEN 'primary'
            WHEN street.tipo IN ('secondary', 'secondary_link') THEN 'secondary'
            WHEN street.tipo IN ('tertiary', 'tertiary_link') THEN 'tertiary'
            WHEN street.tipo IN ('residential', 'living_street', 'unclassified') THEN 'street'
            ELSE NULL
        END AS compatible_category,
        street.id_calle = ANY($3::bigint[]) AS blocked
    FROM vialis.calles street
    CROSS JOIN detour_context context
    WHERE street.geom && ST_Envelope(context.search_area)
      AND ST_Intersects(street.geom, context.search_area)
), matched AS (
    SELECT
        edge.*,
        forward.id AS forward_traffic_id,
        forward.speed_kph AS forward_speed_kph,
        forward.has_speed AS forward_has_speed,
        forward.closure AS forward_closure,
        reverse.id AS reverse_traffic_id,
        reverse.speed_kph AS reverse_speed_kph,
        reverse.has_speed AS reverse_has_speed,
        reverse.closure AS reverse_closure
    FROM candidate_edges edge
    LEFT JOIN LATERAL (
        SELECT candidate.*
        FROM (
            SELECT
                traffic.*,
                ST_Distance(
                    traffic.geom::geography,
                    edge.geom::geography
                ) AS distance_meters,
                cos(
                    ST_Azimuth(ST_StartPoint(traffic.geom), ST_EndPoint(traffic.geom))
                    - ST_Azimuth(ST_StartPoint(edge.geom), ST_EndPoint(edge.geom))
                ) AS direction_cosine
            FROM detour_traffic traffic
            WHERE traffic.geom && ST_Expand(edge.geom, $5::double precision / 90000.0)
              AND ST_DWithin(
                  traffic.geom::geography,
                  edge.geom::geography,
                  $5::double precision
              )
        ) candidate
        WHERE degrees(acos(LEAST(1.0, abs(candidate.direction_cosine)))) <= $6
          AND (
              candidate.road_coverage = 'full'
              OR candidate.road_coverage = 'one_side' AND candidate.direction_cosine >= 0
          )
        ORDER BY candidate.distance_meters, candidate.id
        LIMIT 1
    ) forward ON true
    LEFT JOIN LATERAL (
        SELECT candidate.*
        FROM (
            SELECT
                traffic.*,
                ST_Distance(
                    traffic.geom::geography,
                    edge.geom::geography
                ) AS distance_meters,
                cos(
                    ST_Azimuth(ST_StartPoint(traffic.geom), ST_EndPoint(traffic.geom))
                    - ST_Azimuth(ST_StartPoint(edge.geom), ST_EndPoint(edge.geom))
                ) AS direction_cosine
            FROM detour_traffic traffic
            WHERE edge.reverse_allowed
              AND traffic.geom && ST_Expand(edge.geom, $5::double precision / 90000.0)
              AND ST_DWithin(
                  traffic.geom::geography,
                  edge.geom::geography,
                  $5::double precision
              )
        ) candidate
        WHERE degrees(acos(LEAST(1.0, abs(candidate.direction_cosine)))) <= $6
          AND (
              candidate.road_coverage = 'full'
              OR candidate.road_coverage = 'one_side' AND candidate.direction_cosine < 0
          )
        ORDER BY candidate.distance_meters, candidate.id
        LIMIT 1
    ) reverse ON true
), estimated AS (
    SELECT matched.*, estimate.sample_count, estimate.speed_kph AS estimated_speed_kph
    FROM matched
    LEFT JOIN LATERAL (
        SELECT
            count(*)::integer AS sample_count,
            percentile_cont(0.5) WITHIN GROUP (ORDER BY nearest.speed_kph) AS speed_kph
        FROM (
            SELECT distinct_source.source_id, distinct_source.speed_kph
            FROM (
                SELECT DISTINCT ON (traffic.source_id)
                    traffic.source_id,
                    traffic.speed_kph,
                    ST_Distance(
                        traffic.geom::geography,
                        matched.geom::geography
                    ) AS distance_meters
                FROM detour_traffic traffic
                WHERE matched.compatible_category IS NOT NULL
                  AND traffic.road_category = matched.compatible_category
                  AND traffic.has_speed
                  AND NOT traffic.closure
                  AND traffic.speed_kph > 0
                  AND traffic.geom && ST_Expand(
                      matched.geom,
                      $7::double precision / 90000.0
                  )
                  AND ST_DWithin(
                      traffic.geom::geography,
                      matched.geom::geography,
                      $7::double precision
                  )
                ORDER BY traffic.source_id, distance_meters
            ) distinct_source
            ORDER BY distinct_source.distance_meters, distinct_source.source_id
            LIMIT $8::integer
        ) nearest
    ) estimate ON true
)
SELECT
    id,
    source,
    target,
    CASE
        WHEN blocked THEN -1::double precision
        ELSE original_cost
    END AS topology_cost,
    CASE
        WHEN blocked OR NOT reverse_allowed THEN -1::double precision
        ELSE original_reverse_cost
    END AS topology_reverse_cost,
    CASE
        WHEN blocked THEN -1::double precision
        WHEN forward_traffic_id IS NOT NULL THEN
            CASE
                WHEN forward_closure OR NOT forward_has_speed OR forward_speed_kph <= 0
                THEN -1::double precision
                ELSE length_meters / (forward_speed_kph / 3.6)
            END
        WHEN sample_count >= $9::integer
        THEN length_meters / (estimated_speed_kph / 3.6)
        ELSE -1::double precision
    END AS cost,
    CASE
        WHEN blocked OR NOT reverse_allowed THEN -1::double precision
        WHEN reverse_traffic_id IS NOT NULL THEN
            CASE
                WHEN reverse_closure OR NOT reverse_has_speed OR reverse_speed_kph <= 0
                THEN -1::double precision
                ELSE length_meters / (reverse_speed_kph / 3.6)
            END
        WHEN sample_count >= $9::integer
        THEN length_meters / (estimated_speed_kph / 3.6)
        ELSE -1::double precision
    END AS reverse_cost,
    CASE
        WHEN blocked THEN 'blocked'
        WHEN forward_traffic_id IS NOT NULL THEN 'tomtom_direct'
        WHEN sample_count >= $9::integer THEN 'tomtom_nearby_estimate'
        ELSE 'uncovered'
    END AS forward_source,
    CASE
        WHEN blocked OR NOT reverse_allowed THEN 'not_allowed'
        WHEN reverse_traffic_id IS NOT NULL THEN 'tomtom_direct'
        WHEN sample_count >= $9::integer THEN 'tomtom_nearby_estimate'
        ELSE 'uncovered'
    END AS reverse_source,
    blocked,
    geom
FROM estimated;

CREATE UNIQUE INDEX detour_graph_id_idx ON detour_graph (id);
CREATE INDEX detour_graph_geom_idx ON detour_graph USING GIST (geom);
ANALYZE detour_graph;
