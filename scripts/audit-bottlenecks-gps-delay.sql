-- Read-only: GPS age at FIRST persisted reception, not pure network latency.
-- Existing raw is canonical by vehicle/observed timestamp; repetitions are absent.
-- No coordinates or vehicle IDs are emitted. Fresh<=300s is the existing H0011
-- age limit, used only to describe the sample, not a new Bottlenecks filter.
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';
WITH raw AS MATERIALIZED (
    SELECT observado_en, recibido_en, id_vehiculo, id_captura,
           extract(epoch FROM recibido_en-observado_en)::double precision AS age_s
    FROM vialis.posiciones_colectivos_raw
), profiles AS (
    SELECT 'all_retained' AS sample, * FROM raw
    UNION ALL
    SELECT 'received_last_hour', * FROM raw
    WHERE recibido_en >= (SELECT max(recibido_en)-interval '1 hour' FROM raw)
    UNION ALL
    SELECT 'existing_fresh_age_limit', * FROM raw WHERE age_s BETWEEN 0 AND 300
), age_stats AS (
    SELECT sample, count(*) AS positions, min(observado_en) AS observed_from,
           max(observado_en) AS observed_to, min(recibido_en) AS received_from,
           max(recibido_en) AS received_to, min(age_s) AS min_age_s,
           percentile_cont(ARRAY[0.5,0.9,0.95,0.99]) WITHIN GROUP (ORDER BY age_s) AS p50_p90_p95_p99_age_s,
           max(age_s) AS max_age_s,
           count(*) FILTER (WHERE age_s<0) AS future_clock,
           count(*) FILTER (WHERE age_s BETWEEN 0 AND 30) AS age_0_30s,
           count(*) FILTER (WHERE age_s>30 AND age_s<=60) AS age_30_60s,
           count(*) FILTER (WHERE age_s>60 AND age_s<=120) AS age_60_120s,
           count(*) FILTER (WHERE age_s>120 AND age_s<=180) AS age_120_180s,
           count(*) FILTER (WHERE age_s>180 AND age_s<=300) AS age_180_300s,
           count(*) FILTER (WHERE age_s>300) AS age_over_300s
    FROM profiles GROUP BY sample
), arrival_order AS (
    SELECT *, max(observado_en) OVER (
        PARTITION BY id_vehiculo ORDER BY recibido_en,id_captura,observado_en
        ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
    ) AS previous_latest_observed
    FROM raw
), reordered AS (
    SELECT count(*) FILTER (WHERE observado_en<previous_latest_observed) AS older_than_previously_received,
           max(extract(epoch FROM previous_latest_observed-observado_en))
               FILTER (WHERE observado_en<previous_latest_observed) AS max_event_time_regression_s
    FROM arrival_order
), capture_order AS (
    SELECT *, extract(epoch FROM recibido_en-lag(recibido_en) OVER (ORDER BY recibido_en,id_captura)) AS gap_s,
           extract(epoch FROM recibido_en-lag(recibido_en,3) OVER (ORDER BY recibido_en,id_captura)) AS three_capture_span_s
    FROM vialis.capturas_colectivos
), capture_stats AS (
    SELECT count(*) AS captures, min(recibido_en) AS received_from,max(recibido_en) AS received_to,
           count(*) FILTER (WHERE cobertura_parcial) AS partial_captures,
           sum(filas_validas) AS valid_rows_including_repetitions_and_unmapped,
           sum(filas_stale) AS stale_rows_including_repetitions_and_unmapped,
           percentile_cont(ARRAY[0.5,0.9,0.95,0.99]) WITHIN GROUP (ORDER BY gap_s) AS p50_p90_p95_p99_capture_gap_s,
           max(gap_s) AS max_capture_gap_s,
           percentile_cont(ARRAY[0.5,0.9,0.95,0.99]) WITHIN GROUP (ORDER BY three_capture_span_s) AS p50_p90_p95_p99_three_capture_span_s,
           max(three_capture_span_s) AS max_three_capture_span_s
    FROM capture_order
)
SELECT jsonb_pretty(jsonb_build_object(
    'measurement','first_received_at - gps_observed_at, seconds; not network-only delay',
    'snapshot_at',transaction_timestamp(),
    'ages',(SELECT jsonb_agg(to_jsonb(a) ORDER BY sample) FROM age_stats a),
    'arrival_order',(SELECT to_jsonb(r) FROM reordered r),
    'captures',(SELECT to_jsonb(c) FROM capture_stats c)
));
COMMIT;
