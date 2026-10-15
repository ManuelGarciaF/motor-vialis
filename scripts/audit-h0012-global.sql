-- Global H0012 spatial diagnostics, using the SAME active mapping as H0011.
-- Required: psql -v from=<timestamp> -v to=<timestamp>.
-- Source tables read-only; only session-local TEMP fragments/index are written.
-- No ID equality assumption between GCBA and GTFS; no remapping.
\set ON_ERROR_STOP on
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
-- Catalog-wide search is a separate, optional diagnostic. Disabled by default
-- to bound cost; null counters mean NOT evaluated, never zero missing points.
\if :{?compare_catalog}
\else
\set compare_catalog false
\endif
-- Empty TEMP structure/index are created before the read-only snapshot because
-- PostgreSQL prohibits CREATE TABLE there. No source rows are read yet.
CREATE TEMP TABLE h0012_catalog_fragments(geog geography(LineString,4326));
CREATE INDEX ON h0012_catalog_fragments USING GIST(geog);
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='5min';
\if :compare_catalog
-- Only TEMP rows are written inside this read-only transaction. Every original
-- edge is retained; chunks overlap at existing vertices (no interpolated points).
INSERT INTO pg_temp.h0012_catalog_fragments(geog)
WITH vertices AS (
 SELECT r.id_recorrido,(d).path[1] AS vertex,(d).geom AS point
 FROM vialis.recorridos r CROSS JOIN LATERAL ST_DumpPoints(r.geom) d
), assigned AS (
 SELECT v.*,g.bucket FROM vertices v CROSS JOIN LATERAL (
 SELECT (v.vertex-1)/127 AS bucket
 UNION ALL SELECT (v.vertex-1)/127-1
 WHERE v.vertex>1 AND mod(v.vertex-1,127)=0
 ) g
)
SELECT ST_MakeLine(point ORDER BY vertex)::geography
FROM assigned GROUP BY id_recorrido,bucket HAVING count(*)>=2;
ANALYZE pg_temp.h0012_catalog_fragments;
\endif
WITH active AS MATERIALIZED (
 SELECT e.gcba_route_id,e.gcba_direction,e.gtfs_route_id,e.gtfs_direction_id,
 r.id_recorrido,r.linea,r.ramal,r.geom::geography AS geog,r.gtfs_shape_id,
 md5(encode(ST_AsEWKB(r.geom),'hex')) AS shape_hash
 FROM vialis.equivalencias_recorridos_gcba e
 LEFT JOIN vialis.recorridos r ON r.gtfs_route_id=e.gtfs_route_id
                             AND r.direction_id=e.gtfs_direction_id
), joined AS MATERIALIZED (
 SELECT p.id_vehiculo,p.observado_en,p.gcba_route_id,p.gcba_direction,
 p.posicion,p.velocidad_metros_segundo,
 a.gtfs_route_id,a.gtfs_direction_id,a.id_recorrido,a.linea,a.ramal,
 CASE WHEN a.gcba_route_id IS NULL THEN 'no_active_equivalence'
 WHEN a.id_recorrido IS NULL THEN 'no_catalog_target'
 WHEN p.gtfs_route_id<>a.gtfs_route_id OR p.gtfs_direction_id<>a.gtfs_direction_id THEN 'mapping_changed'
 WHEN p.gtfs_shape_id<>a.gtfs_shape_id OR p.gtfs_shape_hash<>a.shape_hash THEN 'geometry_changed'
 ELSE 'comparable' END AS status
 FROM vialis.posiciones_colectivos_raw p
 LEFT JOIN active a ON a.gcba_route_id=p.gcba_route_id AND a.gcba_direction=p.gcba_direction
 WHERE p.observado_en>=:'from'::timestamptz AND p.observado_en<:'to'::timestamptz
 AND p.dia_observado BETWEEN (:'from'::timestamptz AT TIME ZONE 'UTC')::date
                        AND (:'to'::timestamptz AT TIME ZONE 'UTC')::date
 ORDER BY p.gcba_route_id,p.gcba_direction
), measured AS MATERIALIZED (
 -- Keep the geometry once per active mapping, NOT once per raw observation.
 -- Ordering adjacent scopes also improves geography distance cache reuse.
 SELECT j.*,CASE WHEN j.status='comparable'
 THEN ST_Distance(j.posicion::geography,a.geog) END AS distance_m
 FROM joined j LEFT JOIN active a
 ON a.gcba_route_id=j.gcba_route_id AND a.gcba_direction=j.gcba_direction
), classified AS MATERIALIZED (
 SELECT *,CASE WHEN :'compare_catalog'::boolean AND distance_m>100 THEN EXISTS (
 SELECT 1 FROM pg_temp.h0012_catalog_fragments other
 WHERE ST_DWithin(other.geog,measured.posicion::geography,100)
 ) END AS near_another_shape
 FROM measured
), providers AS MATERIALIZED (
 SELECT gcba_route_id,gcba_direction,gtfs_route_id,gtfs_direction_id,
 id_recorrido,linea,ramal,count(*) raw_positions,
 count(DISTINCT id_vehiculo) vehicles,
 min(observado_en) first_gps,max(observado_en) last_gps,
 count(DISTINCT (observado_en AT TIME ZONE 'America/Argentina/Buenos_Aires')::date) local_dates,
 count(*) FILTER(WHERE status='comparable') comparable_positions,
 count(*) FILTER(WHERE distance_m<=100) within_radius,
 count(*) FILTER(WHERE distance_m>100) outside_radius,
 count(*) FILTER(WHERE distance_m>100 AND velocidad_metros_segundo>0) outside_positive_speed,
 CASE WHEN :'compare_catalog'::boolean THEN count(*) FILTER(WHERE near_another_shape) END outside_near_other_shape,
 CASE WHEN :'compare_catalog'::boolean THEN count(*) FILTER(WHERE near_another_shape=false) END outside_near_no_shape,
 CASE WHEN :'compare_catalog'::boolean THEN count(*) FILTER(WHERE near_another_shape=false AND velocidad_metros_segundo>0) END outside_near_no_shape_positive_speed,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY distance_m) median_distance_m,
 max(distance_m) maximum_distance_m
 FROM classified GROUP BY gcba_route_id,gcba_direction,gtfs_route_id,gtfs_direction_id,id_recorrido,linea,ramal
), targets AS MATERIALIZED (
 SELECT gtfs_route_id,gtfs_direction_id,id_recorrido,linea,ramal,
 count(*) provider_scopes,sum(raw_positions) raw_positions,
 sum(comparable_positions) comparable_positions,sum(within_radius) within_radius,
 sum(outside_radius) outside_radius,
 sum(outside_near_other_shape) outside_near_other_shape,
 sum(outside_near_no_shape) outside_near_no_shape,
 round(sum(outside_radius)::numeric/nullif(sum(comparable_positions),0),6) outside_ratio
 FROM providers GROUP BY gtfs_route_id,gtfs_direction_id,id_recorrido,linea,ramal
), days AS (
 SELECT (observado_en AT TIME ZONE 'America/Argentina/Buenos_Aires')::date local_date,
 count(*) raw_positions,min(observado_en) first_gps,max(observado_en) last_gps,
 count(DISTINCT (gcba_route_id,gcba_direction)) provider_scopes,
 count(*) FILTER(WHERE status='comparable') comparable_positions,
 count(*) FILTER(WHERE distance_m<=100) within_radius,
 count(*) FILTER(WHERE distance_m>100) outside_radius,
 CASE WHEN :'compare_catalog'::boolean THEN count(*) FILTER(WHERE near_another_shape=false) END outside_near_no_shape
 FROM classified GROUP BY 1
), statuses AS (
 SELECT status,count(*) positions FROM classified GROUP BY status
)
SELECT jsonb_pretty(jsonb_build_object(
 'from',:'from','to',:'to','radiusMeters',100,
 'catalogComparisonEnabled',:'compare_catalog'::boolean,
 'catalogFragments',(SELECT count(*) FROM pg_temp.h0012_catalog_fragments),
 'fragmentIntegrity',CASE WHEN :'compare_catalog'::boolean THEN jsonb_build_object(
 'originalEdges',(SELECT sum(ST_NPoints(geom)-1) FROM vialis.recorridos),
 'fragmentEdges',(SELECT sum(ST_NPoints(geog::geometry)-1) FROM pg_temp.h0012_catalog_fragments),
 'originalLengthMeters',(SELECT sum(ST_Length(geom::geography)) FROM vialis.recorridos),
 'fragmentLengthMeters',(SELECT sum(ST_Length(geog)) FROM pg_temp.h0012_catalog_fragments)) END,
 'sourceTransactionReadOnly',current_setting('transaction_read_only')::boolean,
 'mappingTable','vialis.equivalencias_recorridos_gcba',
 'mappingJoin','GCBA route + direction -> explicit GTFS route + direction',
 'activeEquivalences',(SELECT count(*) FROM active),
 'activeTargetsMissingCatalog',(SELECT count(*) FROM active WHERE id_recorrido IS NULL),
 'summary',(SELECT jsonb_build_object(
 'rawPositions',count(*),'vehicles',count(DISTINCT id_vehiculo),
 'providerScopes',count(DISTINCT (gcba_route_id,gcba_direction)),
 'firstGPS',min(observado_en),'lastGPS',max(observado_en),
 'comparablePositions',count(*) FILTER(WHERE status='comparable'),
 'withinRadius',count(*) FILTER(WHERE distance_m<=100),
 'outsideRadius',count(*) FILTER(WHERE distance_m>100),
 'outsidePositiveSpeed',count(*) FILTER(WHERE distance_m>100 AND velocidad_metros_segundo>0),
 'outsideNearOtherShape',CASE WHEN :'compare_catalog'::boolean THEN count(*) FILTER(WHERE near_another_shape) END,
 'outsideNearNoShape',CASE WHEN :'compare_catalog'::boolean THEN count(*) FILTER(WHERE near_another_shape=false) END,
 'outsideNearNoShapePositiveSpeed',CASE WHEN :'compare_catalog'::boolean THEN count(*) FILTER(WHERE near_another_shape=false AND velocidad_metros_segundo>0) END
 ) FROM classified),
 'distribution',(SELECT jsonb_build_object(
 'comparableProviderScopes',count(*) FILTER(WHERE comparable_positions>0),
 'providerScopesWithOutside',count(*) FILTER(WHERE outside_radius>0),
 'medianProviderOutsideRatio',percentile_cont(0.5) WITHIN GROUP(ORDER BY outside_radius::float8/nullif(comparable_positions,0)),
 'p90ProviderOutsideRatio',percentile_cont(0.9) WITHIN GROUP(ORDER BY outside_radius::float8/nullif(comparable_positions,0)),
 'comparableGTFSTargets',(SELECT count(*) FROM targets WHERE comparable_positions>0),
 'GTFSTargetsWithOutside',(SELECT count(*) FROM targets WHERE outside_radius>0)
 ) FROM providers),
 'statuses',(SELECT coalesce(jsonb_agg(to_jsonb(s) ORDER BY status),'[]'::jsonb) FROM statuses s),
 'days',(SELECT coalesce(jsonb_agg(to_jsonb(d) ORDER BY local_date),'[]'::jsonb) FROM days d),
 'providers',(SELECT coalesce(jsonb_agg(to_jsonb(p) ORDER BY gcba_route_id,gcba_direction),'[]'::jsonb) FROM providers p),
 'targets',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY gtfs_route_id,gtfs_direction_id),'[]'::jsonb) FROM targets t),
 'limitations',jsonb_build_array(
 'Only retained mapped raw: unlinked provider positions were never stored',
 'Inactive mappings or historical geometry mismatch are not reprojected',
 'Position counts are not independent trips or fleet coverage',
 'Near another shape does not identify the correct route or direction',
 'Dates with observations do not imply complete temporal capture coverage',
 'Spatial diagnostics only, not recurrent bottleneck detection')
));
ROLLBACK;
