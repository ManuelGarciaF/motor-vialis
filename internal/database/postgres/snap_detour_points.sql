CREATE TEMP TABLE detour_requested_points ON COMMIT DROP AS
SELECT
    (point ->> 'id')::bigint AS id,
    (point ->> 'order')::integer AS route_order,
    (point ->> 'longitude')::double precision AS longitude,
    (point ->> 'latitude')::double precision AS latitude,
    (point ->> 'direction_degrees')::double precision AS direction_degrees,
    (point ->> 'maximum_distance_meters')::double precision AS maximum_distance_meters,
    point ->> 'role' AS role,
    -- Absent for RF05 points, which use the policy tolerance ($2). A segment
    -- waypoint without a bearing sends 180 so it snaps to the nearest edge.
    (point ->> 'direction_tolerance_degrees')::double precision AS direction_tolerance_degrees,
    ST_SetSRID(ST_MakePoint(
        (point ->> 'longitude')::double precision,
        (point ->> 'latitude')::double precision
    ), 4326) AS geom
FROM jsonb_array_elements($1::jsonb) WITH ORDINALITY AS item(point, ordinality);

CREATE TEMP TABLE detour_points ON COMMIT DROP AS
SELECT
    requested.id AS pid,
    snapped.id AS edge_id,
    snapped.fraction,
    'b'::char AS side,
    requested.geom AS requested_geom,
    snapped.snapped_geom
FROM detour_requested_points requested
CROSS JOIN detour_context context
JOIN LATERAL (
    SELECT located.*
    FROM (
        SELECT
            candidate.*,
            ST_LineLocatePoint(
                candidate.geom,
                ST_ClosestPoint(candidate.inside_geom, requested.geom)
            ) AS fraction,
            ST_ClosestPoint(candidate.inside_geom, requested.geom) AS snapped_geom
        FROM (
            SELECT
                edge.id,
                edge.geom,
                edge.blocked,
                clipped.inside_geom,
                ST_Distance(
                    clipped.inside_geom::geography,
                    requested.geom::geography
                ) AS distance_meters,
                -- A blocked edge keeps the directions it had before the cut, so
                -- it still competes for the points that lie on it.
                LEAST(
                    CASE
                        WHEN edge.topology_cost > 0 OR edge.blocked THEN degrees(acos(GREATEST(-1.0, LEAST(
                            1.0,
                            cos(
                                ST_Azimuth(ST_StartPoint(edge.geom), ST_EndPoint(edge.geom))
                                - radians(requested.direction_degrees)
                            )
                        ))))
                        ELSE 181
                    END,
                    CASE
                        WHEN edge.topology_reverse_cost > 0
                          OR edge.blocked AND edge.reverse_allowed THEN degrees(acos(GREATEST(-1.0, LEAST(
                            1.0,
                            cos(
                                ST_Azimuth(ST_EndPoint(edge.geom), ST_StartPoint(edge.geom))
                                - radians(requested.direction_degrees)
                            )
                        ))))
                        ELSE 181
                    END
                ) AS direction_degrees
            FROM detour_graph edge
            CROSS JOIN LATERAL (
                SELECT ST_CollectionExtract(
                    ST_Intersection(edge.geom, context.search_area),
                    2
                ) AS inside_geom
            ) clipped
            WHERE (edge.topology_cost > 0 OR edge.topology_reverse_cost > 0 OR edge.blocked)
              AND NOT ST_IsEmpty(clipped.inside_geom)
              AND edge.geom && ST_Expand(
                  requested.geom,
                  requested.maximum_distance_meters / 90000.0
              )
              AND ST_DWithin(
                  clipped.inside_geom::geography,
                  requested.geom::geography,
                  requested.maximum_distance_meters
              )
        ) candidate
        WHERE candidate.direction_degrees <= COALESCE(
            requested.direction_tolerance_degrees,
            $2::double precision
        )
    ) located
    -- La linea punto->enganche puede asomarse un poco fuera del area: los anclas
    -- de un intervalo caen JUSTO sobre el borde del area (es, por definicion,
    -- donde el recorrido entra/sale), asi que su enganche a la calle de al lado
    -- casi siempre se sale unos centimetros. Con la tolerancia vieja de 1 cm eso
    -- rechazaba la calle correcta y el ancla quedaba sin enganchar
    -- (anchor_not_matched) aunque hubiera una calle perfecta a 0.5 m. 2 m absorbe
    -- ese borde sin admitir un enganche que de verdad se vaya del area (esos se
    -- van muchos metros). Una arista bloqueada saltea estos checks: solo tiene
    -- que ganar el ranking para dejar al punto sin enganchar.
    WHERE located.blocked OR (
        ST_Length(ST_Difference(
            ST_MakeLine(requested.geom, located.snapped_geom),
            context.search_area
          )::geography) <= 2.0
        AND NOT ST_Intersects(
            ST_MakeLine(requested.geom, located.snapped_geom),
            context.forbidden_area
          )
    )
    -- The direction tolerance above already rejects cross streets and the
    -- opposite carriageway; inside it, the closest edge is where the point is.
    -- Ranking by angle first let a slightly better-aligned next block win, so
    -- the path overshot the stop and drove back to it.
    ORDER BY
        located.distance_meters,
        located.direction_degrees,
        requested.route_order,
        located.id
    LIMIT 1
) snapped ON true
-- The point lies on a closed street: no open edge reaches it, and snapping it to
-- the next block would draw the bus driving along the closure.
WHERE snapped.blocked IS NOT TRUE;

CREATE UNIQUE INDEX detour_points_pid_idx ON detour_points (pid);
