-- Experimental Bottlenecks reader. $1: line ID; $2/$3: UTC [from,to);
-- $4: maximum geodesic distance to any local projection (meters).
-- All alternatives within the radius are retained, not just nearest + margin.
WITH shape AS MATERIALIZED (
 SELECT gtfs_route_id,direction_id,gtfs_shape_id,
        md5(encode(ST_AsEWKB(geom),'hex')) hash,geom
 FROM vialis.recorridos WHERE id_recorrido=$1
), pieces AS MATERIALIZED (
 SELECT (d).path[1] AS edge,(d).geom AS geom,
        ST_Length((d).geom::geography) AS length_m
 FROM shape CROSS JOIN LATERAL ST_DumpSegments(shape.geom) d
), edges AS MATERIALIZED (
 SELECT *,coalesce(sum(length_m) OVER (ORDER BY edge ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) AS start_m
 FROM pieces
), raw AS (
 SELECT p.* FROM vialis.posiciones_colectivos_raw p
 JOIN vialis.equivalencias_recorridos_gcba e
 ON e.gcba_route_id=p.gcba_route_id AND e.gcba_direction=p.gcba_direction
 CROSS JOIN shape s
 WHERE e.gtfs_route_id=s.gtfs_route_id AND e.gtfs_direction_id=s.direction_id
   AND p.observado_en >= $2 AND p.observado_en < $3
   AND p.dia_observado BETWEEN ($2::timestamptz AT TIME ZONE 'UTC')::date
                          AND ($3::timestamptz AT TIME ZONE 'UTC')::date
)
SELECT p.id_vehiculo,p.gcba_route_id,p.gcba_direction,p.gtfs_route_id,
       p.gtfs_direction_id,p.gtfs_shape_id,p.gtfs_shape_hash,p.observado_en,
       coalesce(c.progress,ARRAY[]::double precision[]),
       coalesce(c.distances,ARRAY[]::double precision[])
FROM raw p CROSS JOIN shape s
LEFT JOIN LATERAL (
 WITH nearby AS (
   SELECT e.*,ST_ClosestPoint(e.geom::geography,p.posicion::geography)::geometry AS point
   FROM edges e
   WHERE p.gtfs_route_id=s.gtfs_route_id AND p.gtfs_direction_id=s.direction_id
     AND p.gtfs_shape_id=s.gtfs_shape_id AND p.gtfs_shape_hash=s.hash
     AND e.length_m>0 AND ST_DWithin(e.geom::geography,p.posicion::geography,$4)
 ), projected AS (
   SELECT edge,start_m+ST_Length(ST_LineSubstring(geom,0,
           ST_LineLocatePoint(geom,point))::geography) AS progress_m,
          ST_Distance(point::geography,p.posicion::geography) AS distance_m
   FROM nearby
 ), neighbors AS (
   SELECT *,lag(edge) OVER w prev_edge,lead(edge) OVER w next_edge,
          lag(distance_m) OVER w prev_distance,lead(distance_m) OVER w next_distance
   FROM projected WINDOW w AS (ORDER BY edge)
 ), minima AS (
   -- Adjacent edges projecting to the same vertex collapse below. Distinct
   -- local minima on loops/crossings survive for the temporal domain matcher.
   SELECT round(progress_m::numeric,6)::double precision AS progress_m,
          min(distance_m) AS distance_m
   FROM neighbors
   WHERE distance_m <= $4
     AND (prev_edge IS NULL OR prev_edge<>edge-1 OR distance_m<=prev_distance)
     AND (next_edge IS NULL OR next_edge<>edge+1 OR distance_m<=next_distance)
   GROUP BY round(progress_m::numeric,6)
 )
 SELECT array_agg(progress_m ORDER BY progress_m) progress,
        array_agg(distance_m ORDER BY progress_m) distances FROM minima
) c ON true
ORDER BY p.id_vehiculo,p.observado_en;
