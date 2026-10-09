WITH routed AS (
    SELECT *
    FROM pgr_trsp_withPoints(
        'SELECT id, source, target, topology_cost AS cost, topology_reverse_cost AS reverse_cost FROM detour_graph ORDER BY id',
        'SELECT ARRAY[from_edge, to_edge] AS path, ''Infinity''::float8 AS cost FROM detour_restrictions ORDER BY from_edge, to_edge',
        'SELECT pid, edge_id, fraction, side FROM detour_points ORDER BY pid',
        'SELECT source, target FROM detour_combinations ORDER BY source, target',
        'r',
        directed => true,
        -- pgRouting 4.0 drops a restriction when details => true and another
        -- point splits one of its edges: the path comes back through the
        -- forbidden turn. Without details the path lists only the vertices
        -- and its own endpoints, which is all the geometry below needs.
        details => false
    )
), sequenced AS (
    SELECT
        routed.*,
        lead(node) OVER (
            PARTITION BY start_vid, end_vid
            ORDER BY path_seq
        ) AS next_node
    FROM routed
), traversed AS (
    SELECT
        step.start_vid,
        step.end_vid,
        step.path_seq,
        step.edge,
        step.cost,
        step.agg_cost,
        graph.geom,
        CASE
            WHEN step.node < 0 THEN current_point.fraction
            WHEN step.node = graph.source THEN 0::double precision
            WHEN step.node = graph.target THEN 1::double precision
        END AS start_fraction,
        CASE
            WHEN step.next_node < 0 THEN next_point.fraction
            WHEN step.next_node = graph.source THEN 0::double precision
            WHEN step.next_node = graph.target THEN 1::double precision
        END AS end_fraction
    FROM sequenced step
    JOIN detour_graph graph ON graph.id = step.edge
    LEFT JOIN detour_points current_point ON current_point.pid = abs(step.node)
    LEFT JOIN detour_points next_point ON next_point.pid = abs(step.next_node)
    WHERE step.edge <> -1
), pieces AS (
    SELECT
        start_vid,
        end_vid,
        path_seq,
        edge,
        cost,
        agg_cost,
        CASE
            WHEN start_fraction <= end_fraction THEN ST_LineSubstring(
                geom,
                start_fraction,
                end_fraction
            )
            ELSE ST_Reverse(ST_LineSubstring(
                geom,
                end_fraction,
                start_fraction
            ))
        END AS geom
    FROM traversed
    WHERE start_fraction IS NOT NULL
      AND end_fraction IS NOT NULL
      AND start_fraction <> end_fraction
), paths AS (
    SELECT
        -start_vid AS from_point,
        -end_vid AS to_point,
        max(agg_cost + cost) AS travel_seconds,
        array_agg(edge ORDER BY path_seq) AS edge_ids,
        ST_MakeLine(geom ORDER BY path_seq) AS geom
    FROM pieces
    GROUP BY start_vid, end_vid
), connected AS (
    SELECT
        paths.from_point,
        paths.to_point,
        paths.travel_seconds,
        paths.edge_ids,
        ST_MakeLine(ARRAY[
            ST_MakeLine(origin.requested_geom, origin.snapped_geom),
            paths.geom,
            ST_MakeLine(destination.snapped_geom, destination.requested_geom)
        ]) AS geom
    FROM paths
    JOIN detour_points origin ON origin.pid = paths.from_point
    JOIN detour_points destination ON destination.pid = paths.to_point
)
SELECT
    connected.from_point,
    connected.to_point
FROM connected
CROSS JOIN detour_context context
-- Keep the diagnostic graph under the same boundary rule as traffic routing.
WHERE ST_Length(ST_Difference(
        connected.geom,
        context.search_area
      )::geography) <= 0.01
  AND NOT ST_Intersects(connected.geom, context.forbidden_area)
ORDER BY connected.from_point, connected.to_point;
