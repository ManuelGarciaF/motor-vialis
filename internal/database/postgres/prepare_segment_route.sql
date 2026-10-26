-- Bounds one segment request before the shared detour graph is built: the
-- search area is the waypoints' bounding box grown by $4 metres, and the cut
-- ($2, NULL when there is none) blocks every street inside its forbidden
-- corridor ($3 metres), exactly as analyze_detour.sql does for RF05.
WITH input AS (
    SELECT
        ST_SetSRID(ST_GeomFromGeoJSON($1::jsonb), 4326) AS waypoints,
        CASE
            WHEN $2::jsonb IS NULL THEN NULL
            ELSE ST_SetSRID(ST_GeomFromGeoJSON($2::jsonb), 4326)
        END AS cut
), zones AS (
    SELECT
        ST_Buffer(
            ST_Envelope(input.waypoints)::geography,
            $4::double precision
        )::geometry AS search_area,
        CASE
            WHEN input.cut IS NULL THEN ST_GeomFromText('GEOMETRYCOLLECTION EMPTY', 4326)
            ELSE ST_Buffer(input.cut::geography, $3::double precision)::geometry
        END AS forbidden_area
    FROM input
), blocked AS (
    SELECT COALESCE(array_agg(street.id_calle ORDER BY street.id_calle), '{}') AS ids
    FROM zones
    JOIN vialis.calles street
      ON street.geom && ST_Envelope(zones.forbidden_area)
     AND street.geom && ST_Envelope(zones.search_area)
     AND ST_Intersects(street.geom, zones.forbidden_area)
)
SELECT
    ST_AsGeoJSON(zones.search_area, 15)::bytea,
    ST_AsGeoJSON(zones.forbidden_area, 15)::bytea,
    blocked.ids
FROM zones
CROSS JOIN blocked;
