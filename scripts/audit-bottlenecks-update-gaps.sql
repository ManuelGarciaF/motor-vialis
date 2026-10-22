-- Hypothetical 3-capture expiry sensitivity, not a new closure policy.
-- Counts stored non-partial captures; HTTP failures are not stored captures.
-- Canonical raw only represents NEW vehicle/timestamp rows, not snapshot presence.
-- Temporal compatibility alone does not establish a valid spatial passage.
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';
WITH captures AS MATERIALIZED (
 SELECT id_captura,recibido_en,cobertura_parcial,
        count(*) FILTER (WHERE NOT cobertura_parcial) OVER (ORDER BY recibido_en,id_captura) AS complete_number,
        extract(epoch FROM recibido_en-lag(recibido_en) OVER (ORDER BY recibido_en,id_captura)) AS gap_s,
        extract(epoch FROM recibido_en-lag(recibido_en,3) OVER (ORDER BY recibido_en,id_captura)) AS three_span_s
 FROM vialis.capturas_colectivos
), positions AS (
 SELECT p.id_vehiculo,p.observado_en,p.recibido_en,p.id_captura,c.cobertura_parcial,c.complete_number,
        lag(p.observado_en) OVER w AS previous_observed,
        lag(p.recibido_en) OVER w AS previous_received,
        lag(c.complete_number) OVER w AS previous_complete_number,
        ROW(p.gcba_route_id,p.gcba_direction,p.gtfs_route_id,p.gtfs_direction_id,p.gtfs_shape_id,p.gtfs_shape_hash)
        =lag(ROW(p.gcba_route_id,p.gcba_direction,p.gtfs_route_id,p.gtfs_direction_id,p.gtfs_shape_id,p.gtfs_shape_hash)) OVER w AS same_scope
 FROM vialis.posiciones_colectivos_raw p JOIN captures c USING(id_captura)
 WINDOW w AS (PARTITION BY p.id_vehiculo ORDER BY p.recibido_en,p.id_captura,p.observado_en)
), compatible_pairs AS (
 SELECT *, greatest(0,complete_number-previous_complete_number-CASE WHEN cobertura_parcial THEN 0 ELSE 1 END) AS missing_complete_captures
 FROM positions
 WHERE same_scope AND observado_en>previous_observed
   AND observado_en-previous_observed<=interval '180 seconds'
   AND recibido_en-observado_en BETWEEN interval '0 seconds' AND interval '300 seconds'
   AND previous_received-previous_observed BETWEEN interval '0 seconds' AND interval '300 seconds'
), sample_pairs AS (
 SELECT 'all_retained' AS sample,* FROM compatible_pairs
 UNION ALL
 SELECT 'received_last_hour',* FROM compatible_pairs
 WHERE recibido_en>=(SELECT max(recibido_en)-interval '1 hour' FROM captures)
), sensitivity AS (
 SELECT sample,count(*) AS temporally_compatible_pairs,
        count(*) FILTER (WHERE missing_complete_captures>=3) AS pairs_arriving_after_three_missing_captures,
        count(*) FILTER (WHERE missing_complete_captures>=4) AS pairs_arriving_after_four_missing_captures,
        max(missing_complete_captures) AS maximum_missing_complete_captures
 FROM sample_pairs GROUP BY sample
), latest_cadence AS (
 SELECT count(*) AS captures, min(recibido_en) AS received_from,max(recibido_en) AS received_to,
        percentile_cont(ARRAY[0.5,0.9,0.95,0.99]) WITHIN GROUP(ORDER BY gap_s) AS p50_p90_p95_p99_capture_gap_s,
        percentile_cont(ARRAY[0.5,0.9,0.95,0.99]) WITHIN GROUP(ORDER BY three_span_s) AS p50_p90_p95_p99_three_capture_span_s
 FROM captures WHERE recibido_en>=(SELECT max(recibido_en)-interval '1 hour' FROM captures)
)
SELECT jsonb_pretty(jsonb_build_object(
 'snapshot_at',transaction_timestamp(),
 'latest_hour_cadence',(SELECT to_jsonb(c) FROM latest_cadence c),
 'hypothetical_closure_sensitivity',(SELECT jsonb_agg(to_jsonb(s) ORDER BY sample) FROM sensitivity s),
 'limitations','Counts NEW canonical GPS updates, not disappearance from snapshots. Non-partial stored captures only. Compatible pairs are not confirmed passages; expiry does not imply congestion.'
));
COMMIT;
