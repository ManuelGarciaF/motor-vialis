-- Routes every leg of create_cut_path_graph.sql over the undirected graph and
-- returns each one as a LineString from its snapped origin to its snapped
-- destination, every edge oriented the way the leg traverses it. A leg missing
-- from the result has no path inside the local graph.
WITH routed AS (
    SELECT *
    FROM pgr_withPoints(
        'SELECT id, source, target, cost, cost AS reverse_cost FROM cut_path_graph ORDER BY id',
        'SELECT pid, edge_id, fraction FROM cut_path_points ORDER BY pid',
        'SELECT -from_pid AS source, -to_pid AS target FROM cut_path_legs ORDER BY from_pid',
        directed => false,
        -- Without details a leg does not stop at the other waypoints that lie
        -- on its edges; only its own endpoints appear as points.
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
    JOIN cut_path_graph graph ON graph.id = step.edge
    LEFT JOIN cut_path_points current_point ON current_point.pid = -step.node
    LEFT JOIN cut_path_points next_point ON next_point.pid = -step.next_node
    WHERE step.edge <> -1
), pieces AS (
    SELECT
        start_vid,
        end_vid,
        path_seq,
        CASE
            WHEN start_fraction <= end_fraction THEN ST_LineSubstring(geom, start_fraction, end_fraction)
            ELSE ST_Reverse(ST_LineSubstring(geom, end_fraction, start_fraction))
        END AS geom
    FROM traversed
    WHERE start_fraction IS NOT NULL
      AND end_fraction IS NOT NULL
      AND start_fraction <> end_fraction
), routed_legs AS (
    SELECT
        -start_vid AS from_pid,
        -end_vid AS to_pid,
        ST_MakeLine(geom ORDER BY path_seq) AS geom
    FROM pieces
    GROUP BY start_vid, end_vid
), still_legs AS (
    -- Two waypoints snapped onto the same spot: pgRouting returns no path for
    -- them, but the leg exists and has no length.
    SELECT
        leg.from_pid,
        leg.to_pid,
        ST_MakeLine(origin.snapped_geom, destination.snapped_geom) AS geom
    FROM cut_path_legs leg
    JOIN cut_path_points origin ON origin.pid = leg.from_pid
    JOIN cut_path_points destination ON destination.pid = leg.to_pid
    WHERE origin.edge_id = destination.edge_id
      AND origin.fraction = destination.fraction
)
SELECT from_pid, to_pid, ST_AsGeoJSON(geom)::bytea
FROM routed_legs
UNION ALL
SELECT from_pid, to_pid, ST_AsGeoJSON(geom)::bytea
FROM still_legs
ORDER BY 1;
