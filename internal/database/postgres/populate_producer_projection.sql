-- $1 model; $2 exact snapshot IDs needed by current reception and all OPEN
-- sequences. Build their original edges once, before any per-vehicle replay.
WITH pieces AS MATERIALIZED (
    SELECT t.id, (d).path[1] edge, (d).geom geom,
           ST_Length((d).geom::geography) length_m
    FROM vialis.bottlenecks_trazados t
    CROSS JOIN LATERAL ST_DumpSegments(t.geom) d
    WHERE t.modelo = $1 AND t.id = ANY($2::bigint[])
)
INSERT INTO pg_temp.bottlenecks_projection_edges (layout_id, edge, geom, geog, length_m, start_m)
SELECT id, edge, geom, geom::geography, length_m,
       coalesce(sum(length_m) OVER (
           PARTITION BY id ORDER BY edge
           ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
       ), 0)
FROM pieces;
