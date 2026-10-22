-- Geometric candidates only: no replacement mapping is generated or applied.
-- Required psql vars: line_id, from, to. Radius fixed to approved 100m.
\set ON_ERROR_STOP on
\if :{?line_id}
\else
\echo 'Required: -v line_id=<stored recorrido ID>'
\quit 3
\endif
\if :{?from}
\else
\echo 'Required: -v from=<RFC3339 timestamp>'
\quit 3
\endif
\if :{?to}
\else
\echo 'Required: -v to=<RFC3339 timestamp>'
\quit 3
\endif
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='5min';
WITH selected AS (
 SELECT * FROM vialis.recorridos WHERE id_recorrido=:'line_id'::bigint
), raw AS MATERIALIZED (
 SELECT p.*,ST_Distance(p.posicion::geography,s.geom::geography)>100 AS outside_assigned_shape
 FROM vialis.posiciones_colectivos_raw p CROSS JOIN selected s
 WHERE p.gtfs_route_id=s.gtfs_route_id AND p.gtfs_direction_id=s.direction_id
 AND p.observado_en>=:'from'::timestamptz AND p.observado_en<:'to'::timestamptz
 AND p.dia_observado BETWEEN (:'from'::timestamptz AT TIME ZONE 'UTC')::date
 AND (:'to'::timestamptz AT TIME ZONE 'UTC')::date
), totals AS (
 SELECT gcba_route_id,gcba_direction,count(*) raw_positions,
 count(*) FILTER(WHERE velocidad_metros_segundo>0) positive_speed_positions,
 count(*) FILTER(WHERE outside_assigned_shape) outside_positions,
 count(DISTINCT id_vehiculo) vehicles
 FROM raw GROUP BY gcba_route_id,gcba_direction
), spatial AS MATERIALIZED (
 SELECT p.gcba_route_id,p.gcba_direction,p.observado_en,p.id_vehiculo,p.velocidad_metros_segundo,p.outside_assigned_shape,
 r.id_recorrido,r.gtfs_route_id,r.direction_id,r.linea,r.ramal,
 ST_Distance(p.posicion::geography,r.geom::geography) AS distance_m
 FROM raw p JOIN vialis.recorridos r
 ON ST_DWithin(r.geom::geography,p.posicion::geography,100)
), covered AS (
 SELECT DISTINCT id_vehiculo,observado_en FROM spatial
), catalog_coverage AS (
 SELECT p.gcba_route_id,p.gcba_direction,
 count(*) FILTER(WHERE c.id_vehiculo IS NOT NULL) positions_near_any_shape,
 count(*) FILTER(WHERE p.outside_assigned_shape AND c.id_vehiculo IS NOT NULL) outside_near_another_shape,
 count(*) FILTER(WHERE p.outside_assigned_shape AND c.id_vehiculo IS NULL) outside_near_no_shape,
 count(*) FILTER(WHERE p.outside_assigned_shape AND c.id_vehiculo IS NULL AND p.velocidad_metros_segundo>0) outside_near_no_shape_positive_speed
 FROM raw p LEFT JOIN covered c USING(id_vehiculo,observado_en)
 GROUP BY p.gcba_route_id,p.gcba_direction
), candidates AS (
 SELECT gcba_route_id,gcba_direction,id_recorrido,gtfs_route_id,direction_id,linea,ramal,
 count(*) matched_positions,count(*) FILTER(WHERE velocidad_metros_segundo>0) positive_speed_matches,
 count(*) FILTER(WHERE outside_assigned_shape) matched_outside_assigned_shape,
 count(DISTINCT id_vehiculo) matched_vehicles,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY distance_m) median_distance_m
 FROM spatial GROUP BY gcba_route_id,gcba_direction,id_recorrido,gtfs_route_id,direction_id,linea,ramal
), ranked AS (
 SELECT c.*,t.raw_positions,t.positive_speed_positions,t.outside_positions,t.vehicles,
 round(c.matched_positions::numeric/t.raw_positions,4) AS coverage_ratio,
 row_number() OVER(PARTITION BY gcba_route_id,gcba_direction
 ORDER BY matched_positions DESC,positive_speed_matches DESC,median_distance_m,id_recorrido) AS rank
 FROM candidates c JOIN totals t USING(gcba_route_id,gcba_direction)
)
SELECT jsonb_pretty(jsonb_build_object('lineId',:'line_id'::bigint,'from',:'from','to',:'to',
 'radiusMeters',100,'method','All observed positions vs all stored shapes; geography',
 'limitations',jsonb_build_array('Geometric overlap does not establish identity or circulation direction',
 'Positions are not independent trips; stopped vehicles can dominate counts',
 'Different shapes share corridors; no automatic replacement mapping',
 'Only retained mapped raw for the selected GTFS target, not all provider history'),
 'providers',(SELECT jsonb_agg(to_jsonb(t)||to_jsonb(c) ORDER BY t.gcba_route_id,t.gcba_direction)
 FROM totals t JOIN catalog_coverage c USING(gcba_route_id,gcba_direction)),
 'candidates',(SELECT jsonb_agg(to_jsonb(r) ORDER BY gcba_route_id,gcba_direction,rank) FROM ranked r
 WHERE rank<=10 OR id_recorrido=:'line_id'::bigint)
));
ROLLBACK;
