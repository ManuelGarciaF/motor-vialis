-- $1: snapshot del trazado; $2: ID transitorio del catálogo; $3: radio del modelo.
-- Se guardan alternativas locales, no una parada proyectada arbitrariamente.
WITH pieces AS MATERIALIZED (
 SELECT (d).path[1] edge,(d).geom geom,ST_Length((d).geom::geography) length_m
 FROM vialis.bottlenecks_trazados t CROSS JOIN LATERAL ST_DumpSegments(t.geom) d WHERE t.id=$1
), edges AS MATERIALIZED (
 SELECT *,coalesce(sum(length_m) OVER(ORDER BY edge ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0) start_m FROM pieces
)
SELECT coalesce(jsonb_agg(jsonb_build_object(
 'order',rp.nro_parada,'stopID',p.gtfs_stop_id,'name',p.nombre,
 'position',ST_AsGeoJSON(p.posicion)::jsonb,
 'projectionCandidates',coalesce(c.candidates,'[]'::jsonb)) ORDER BY rp.nro_parada),'[]'::jsonb)
FROM vialis.recorridos_paradas rp JOIN vialis.paradas p USING(id_parada)
LEFT JOIN LATERAL (
 WITH projected AS (
 SELECT edge,round((start_m+ST_Length(ST_LineSubstring(geom,0,ST_LineLocatePoint(geom,point))::geography))::numeric,6)::float8 progress_m,
        ST_Distance(point::geography,p.posicion::geography) distance_m
 FROM edges e CROSS JOIN LATERAL (SELECT ST_ClosestPoint(e.geom::geography,p.posicion::geography)::geometry point) q
 WHERE length_m>0 AND ST_DWithin(e.geom::geography,p.posicion::geography,$3)
 ), neighbors AS (
 SELECT *,lag(edge) OVER w prev_edge,lead(edge) OVER w next_edge,
 lag(distance_m) OVER w prev_distance,lead(distance_m) OVER w next_distance
 FROM projected WINDOW w AS (ORDER BY edge)
 ), minima AS (
 SELECT progress_m,min(distance_m) distance_m FROM neighbors
 WHERE distance_m<=$3 AND (prev_edge IS NULL OR prev_edge<>edge-1 OR distance_m<=prev_distance)
 AND (next_edge IS NULL OR next_edge<>edge+1 OR distance_m<=next_distance)
 GROUP BY progress_m
 )
 SELECT jsonb_agg(jsonb_build_object('progressMeters',progress_m,'distanceMeters',distance_m,
 'sectorIndex',(SELECT indice FROM vialis.bottlenecks_sectores s WHERE s.trazado=$1
 AND (progress_m<s.fin_metros OR s.indice=(SELECT max(indice) FROM vialis.bottlenecks_sectores WHERE trazado=$1))
 ORDER BY indice LIMIT 1)) ORDER BY progress_m) candidates FROM minima
) c ON true
WHERE rp.id_recorrido=$2;
