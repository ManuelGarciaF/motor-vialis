WITH input AS (
    SELECT
        ST_SetSRID(ST_GeomFromGeoJSON($1::jsonb), 4326)
            ::geometry(LineString, 4326) AS cut,
        $2::double precision AS forbidden_radius_meters,
        $3::double precision AS search_radius_meters
), active_graph AS (
    SELECT id_carga, alcance
    FROM vialis.calles_metadata
    WHERE activa
), zones AS (
    SELECT
        graph.id_carga,
        ST_Intersects(graph.alcance, input.cut) AS scope_intersects,
        ST_Buffer(
            input.cut::geography,
            input.forbidden_radius_meters
        )::geometry AS forbidden_area,
        ST_Buffer(
            input.cut::geography,
            input.search_radius_meters
        )::geometry AS search_area
    FROM input
    CROSS JOIN active_graph graph
), route_segments AS (
    SELECT
        (segment ->> 'order')::integer AS segment_order,
        ST_SetSRID(
            ST_GeomFromGeoJSON(segment -> 'path'),
            4326
        )::geometry(LineString, 4326) AS geom
    FROM jsonb_array_elements($4::jsonb) AS segment
), blocked AS (
    SELECT COALESCE(array_agg(street.id_calle ORDER BY street.id_calle), '{}') AS ids
    FROM zones
    JOIN vialis.calles street
      ON street.geom && ST_Envelope(zones.forbidden_area)
     AND ST_Intersects(street.geom, zones.forbidden_area)
), affected AS (
    SELECT EXISTS (
        SELECT 1
        FROM route_segments segment
        CROSS JOIN zones
        WHERE segment.geom && ST_Envelope(zones.forbidden_area)
          AND ST_Intersects(segment.geom, zones.forbidden_area)
    ) AS value
), inside_parts AS (
    SELECT
        segment.segment_order,
        segment.geom AS segment_geom,
        (dumped).geom::geometry(LineString, 4326) AS inside_geom
    FROM route_segments segment
    CROSS JOIN zones
    CROSS JOIN LATERAL ST_Dump(
        ST_CollectionExtract(
            ST_Intersection(segment.geom, zones.search_area),
            2
        )
    ) AS dumped
    WHERE segment.geom && ST_Envelope(zones.search_area)
), located_parts AS (
    SELECT
        segment_order,
        segment_geom,
        LEAST(
            ST_LineLocatePoint(segment_geom, ST_StartPoint(inside_geom)),
            ST_LineLocatePoint(segment_geom, ST_EndPoint(inside_geom))
        ) AS start_fraction,
        GREATEST(
            ST_LineLocatePoint(segment_geom, ST_StartPoint(inside_geom)),
            ST_LineLocatePoint(segment_geom, ST_EndPoint(inside_geom))
        ) AS end_fraction
    FROM inside_parts
    WHERE ST_Length(inside_geom::geography) > 0
), anchors AS (
    SELECT
        segment_order,
        start_fraction,
        end_fraction,
        ST_LineInterpolatePoint(segment_geom, start_fraction) AS start_point,
        ST_LineInterpolatePoint(segment_geom, end_fraction) AS end_point,
        degrees(ST_Azimuth(
            ST_LineInterpolatePoint(segment_geom, GREATEST(0, start_fraction - 0.001)),
            ST_LineInterpolatePoint(segment_geom, LEAST(1, start_fraction + 0.001))
        )) AS start_direction,
        degrees(ST_Azimuth(
            ST_LineInterpolatePoint(segment_geom, GREATEST(0, end_fraction - 0.001)),
            ST_LineInterpolatePoint(segment_geom, LEAST(1, end_fraction + 0.001))
        )) AS end_direction
    FROM located_parts
)
SELECT
    zones.id_carga,
    zones.scope_intersects,
    affected.value AS route_affected,
    ST_AsGeoJSON(zones.search_area, 15)::bytea AS search_area,
    ST_AsGeoJSON(zones.forbidden_area, 15)::bytea AS forbidden_area,
    ST_XMin(Box3D(zones.search_area)) AS minimum_longitude,
    ST_YMin(Box3D(zones.search_area)) AS minimum_latitude,
    ST_XMax(Box3D(zones.search_area)) AS maximum_longitude,
    ST_YMax(Box3D(zones.search_area)) AS maximum_latitude,
    blocked.ids AS blocked_street_ids,
    anchors.segment_order,
    anchors.start_fraction,
    anchors.end_fraction,
    ST_X(anchors.start_point) AS start_longitude,
    ST_Y(anchors.start_point) AS start_latitude,
    anchors.start_direction,
    ST_X(anchors.end_point) AS end_longitude,
    ST_Y(anchors.end_point) AS end_latitude,
    anchors.end_direction
FROM zones
CROSS JOIN blocked
CROSS JOIN affected
LEFT JOIN anchors ON true
ORDER BY anchors.segment_order, anchors.start_fraction;
