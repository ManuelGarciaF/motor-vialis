-- Read-only diagnostics; does not change matching/measurement policy.
-- Required psql variables: line_id, from, to. Optional radius defaults to 100m.
\set ON_ERROR_STOP on
\if :{?line_id}
\else
\echo 'Required: -v line_id=<stored recorrido ID>'
\quit 3
\endif
\if :{?from}
\else
\echo 'Required: -v from=<RFC3339 GPS timestamp>'
\quit 3
\endif
\if :{?to}
\else
\echo 'Required: -v to=<RFC3339 GPS timestamp>'
\quit 3
\endif
\if :{?radius}
\else
\set radius 100
\endif
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='5min';
WITH shape AS MATERIALIZED (
 SELECT id_recorrido,linea,ramal,gtfs_route_id,direction_id,gtfs_shape_id,
 md5(encode(ST_AsEWKB(geom),'hex')) hash,geom
 FROM vialis.recorridos WHERE id_recorrido=:'line_id'::bigint
), positions AS MATERIALIZED (
 SELECT p.gcba_route_id,p.gcba_direction,p.velocidad_metros_segundo,
 p.gtfs_shape_id=s.gtfs_shape_id AND p.gtfs_shape_hash=s.hash AS current_version,
 ST_Distance(p.posicion::geography,s.geom::geography) AS distance_m,
 p.observado_en
 FROM vialis.posiciones_colectivos_raw p CROSS JOIN shape s
 WHERE p.gtfs_route_id=s.gtfs_route_id AND p.gtfs_direction_id=s.direction_id
 AND p.observado_en>=:'from'::timestamptz AND p.observado_en<:'to'::timestamptz
 AND p.dia_observado BETWEEN (:'from'::timestamptz AT TIME ZONE 'UTC')::date
 AND (:'to'::timestamptz AT TIME ZONE 'UTC')::date
), providers AS (
 SELECT gcba_route_id,gcba_direction,count(*) raw_positions,
 count(*) FILTER(WHERE current_version) current_version,
 count(*) FILTER(WHERE current_version AND distance_m<=:'radius'::float8) within_radius,
 count(*) FILTER(WHERE current_version AND distance_m>:'radius'::float8) outside_radius,
 min(distance_m) min_distance_m,max(distance_m) max_distance_m,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY distance_m) median_distance_m,
 count(*) FILTER(WHERE velocidad_metros_segundo=0) zero_speed_positions,
 count(*) FILTER(WHERE current_version AND distance_m>:'radius'::float8
                 AND velocidad_metros_segundo>0) outside_radius_positive_speed
 FROM positions GROUP BY gcba_route_id,gcba_direction
), bands AS (
 SELECT CASE WHEN distance_m<=25 THEN '01: <=25m'
 WHEN distance_m<=50 THEN '02: (25,50]m'
 WHEN distance_m<=100 THEN '03: (50,100]m'
 WHEN distance_m<=300 THEN '04: (100,300]m'
 WHEN distance_m<=800 THEN '05: (300,800]m'
 WHEN distance_m<=2000 THEN '06: (800,2000]m'
 ELSE '07: >2000m' END band,count(*) positions
 FROM positions GROUP BY 1
)
SELECT jsonb_pretty(jsonb_build_object(
 'lineId',:'line_id'::bigint,'from',:'from','to',:'to','radiusMeters',:'radius'::float8,
 'shape',(SELECT jsonb_build_object('line',linea,'branch',ramal,'direction',direction_id,
 'gtfsRouteId',gtfs_route_id,'shapeId',gtfs_shape_id,'shapeHash',hash,
 'lengthMeters',ST_Length(geom::geography)) FROM shape),
 'coverage',(SELECT jsonb_build_object('rawPositions',count(*),
 'currentVersion',count(*) FILTER(WHERE current_version),
 'withinRadius',count(*) FILTER(WHERE current_version AND distance_m<=:'radius'::float8),
 'outsideRadius',count(*) FILTER(WHERE current_version AND distance_m>:'radius'::float8),
 'outsideRadiusPositiveSpeed',count(*) FILTER(WHERE current_version AND distance_m>:'radius'::float8 AND velocidad_metros_segundo>0),
 'minimumDistanceMeters',min(distance_m),'maximumDistanceMeters',max(distance_m),
 'medianDistanceMeters',percentile_cont(0.5) WITHIN GROUP(ORDER BY distance_m),
 'localDates',count(DISTINCT (observado_en AT TIME ZONE 'America/Argentina/Buenos_Aires')::date)) FROM positions),
 'providers',(SELECT coalesce(jsonb_agg(to_jsonb(p) ORDER BY gcba_route_id,gcba_direction),'[]'::jsonb) FROM providers p),
 'distanceBands',(SELECT coalesce(jsonb_agg(to_jsonb(b) ORDER BY band),'[]'::jsonb) FROM bands b)
));
ROLLBACK;
