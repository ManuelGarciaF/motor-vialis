\set ON_ERROR_STOP on
-- One-off histórico para las columnas incorporadas en H0011.
-- Por defecto sólo muestra un preview y hace ROLLBACK.
-- Aplicar explícitamente: psql "$DATABASE_URL" -X -v apply=true -f scripts/backfill-bunching-speeds.sql
\if :{?apply}
\else
    \set apply false
\endif

BEGIN;
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '60s';

\if :apply
    -- Mismo lock que ingesta/mantenimiento: evita perder máximos nuevos o que
    -- desaparezcan particiones mientras se calcula y actualiza el backfill.
    SELECT pg_advisory_xact_lock(110011);
\endif

-- Aproximación conservadora, no replay del detector: raw no conserva la
-- pertenencia completa de cada posición a cada snapshot admitido del par.
-- Se excluyen capturas parciales/no analizadas y GPS stale/futuro inválido.
-- El máximo sólo usa la misma identidad operacional y versión geométrica.
-- Para cada unidad se corta en SU último timestamp admitido; cerrado_en nunca
-- extiende el intervalo. Los máximos actuales nunca disminuyen.
-- Límites de frescura: config.GCBAMaximumPositionAge / GCBAMaximumFutureSkew.
CREATE TEMP TABLE bunching_speed_backfill ON COMMIT DROP AS
SELECT
    e.id_evento,
    e.gtfs_route_id,
    e.gtfs_direction_id,
    e.observaciones,
    EXTRACT(EPOCH FROM (e.ultimo_observado_en - e.primero_observado_en)) AS duration_seconds,
    e.velocidad_maxima_a_metros_segundo AS before_a,
    e.velocidad_maxima_b_metros_segundo AS before_b,
    a.samples AS samples_a,
    b.samples AS samples_b,
    GREATEST(e.velocidad_maxima_a_metros_segundo, COALESCE(a.maximum_speed, 0)) AS after_a,
    GREATEST(e.velocidad_maxima_b_metros_segundo, COALESCE(b.maximum_speed, 0)) AS after_b
FROM vialis.aglutinaciones_colectivos e
CROSS JOIN LATERAL (
    SELECT count(*) AS samples, max(p.velocidad_metros_segundo) AS maximum_speed
    FROM vialis.posiciones_colectivos_raw p
    JOIN vialis.capturas_colectivos c ON c.id_captura = p.id_captura
    WHERE p.dia_observado BETWEEN
          (e.primero_observado_en AT TIME ZONE 'UTC')::date
          AND (COALESCE(e.observado_a_en, e.ultimo_observado_en) AT TIME ZONE 'UTC')::date
      AND p.id_vehiculo = e.id_vehiculo_a
      AND p.observado_en >= e.primero_observado_en
      AND p.observado_en <= COALESCE(e.observado_a_en, e.ultimo_observado_en)
      AND p.gcba_route_id = e.gcba_route_id
      AND p.gcba_direction = e.gcba_direction
      AND p.gtfs_route_id = e.gtfs_route_id
      AND p.gtfs_direction_id = e.gtfs_direction_id
      AND p.gtfs_shape_id = e.gtfs_shape_id
      AND p.gtfs_shape_hash = e.gtfs_shape_hash
      AND c.aglutinaciones_analizadas AND NOT c.cobertura_parcial
      AND p.observado_en >= c.recibido_en - interval '5 minutes'
      AND p.observado_en <= c.recibido_en + interval '1 minute'
) a
CROSS JOIN LATERAL (
    SELECT count(*) AS samples, max(p.velocidad_metros_segundo) AS maximum_speed
    FROM vialis.posiciones_colectivos_raw p
    JOIN vialis.capturas_colectivos c ON c.id_captura = p.id_captura
    WHERE p.dia_observado BETWEEN
          (e.primero_observado_en AT TIME ZONE 'UTC')::date
          AND (COALESCE(e.observado_b_en, e.ultimo_observado_en) AT TIME ZONE 'UTC')::date
      AND p.id_vehiculo = e.id_vehiculo_b
      AND p.observado_en >= e.primero_observado_en
      AND p.observado_en <= COALESCE(e.observado_b_en, e.ultimo_observado_en)
      AND p.gcba_route_id = e.gcba_route_id
      AND p.gcba_direction = e.gcba_direction
      AND p.gtfs_route_id = e.gtfs_route_id
      AND p.gtfs_direction_id = e.gtfs_direction_id
      AND p.gtfs_shape_id = e.gtfs_shape_id
      AND p.gtfs_shape_hash = e.gtfs_shape_hash
      AND c.aglutinaciones_analizadas AND NOT c.cobertura_parcial
      AND p.observado_en >= c.recibido_en - interval '5 minutes'
      AND p.observado_en <= c.recibido_en + interval '1 minute'
) b;

-- Los criterios de publicación coinciden con los defaults del motor:
-- >=2 observaciones, >=900 segundos y máximos de ambas unidades >=1 m/s.
SELECT
    count(*) AS events_scanned,
    count(*) FILTER (WHERE samples_a > 0 AND samples_b > 0) AS events_with_raw_for_both,
    count(*) FILTER (WHERE samples_a = 0 OR samples_b = 0) AS events_with_incomplete_raw,
    count(*) FILTER (WHERE after_a > before_a OR after_b > before_b) AS events_to_update,
    count(*) FILTER (WHERE observaciones >= 2 AND duration_seconds >= 900) AS duration_qualified,
    count(*) FILTER (
        WHERE observaciones >= 2 AND duration_seconds >= 900
          AND (samples_a = 0 OR samples_b = 0)
    ) AS duration_qualified_with_incomplete_raw,
    count(*) FILTER (
        WHERE observaciones >= 2 AND duration_seconds >= 900
          AND before_a >= 1 AND before_b >= 1
    ) AS eligible_before,
    count(*) FILTER (
        WHERE observaciones >= 2 AND duration_seconds >= 900
          AND after_a >= 1 AND after_b >= 1
    ) AS eligible_after
FROM bunching_speed_backfill;

-- Verificación focalizada del recorrido 1714 (194 C, sentido 1).
SELECT
    r.id_recorrido,
    r.linea,
    r.ramal,
    count(*) FILTER (WHERE b.observaciones >= 2 AND b.duration_seconds >= 900) AS duration_qualified,
    count(*) FILTER (
        WHERE b.observaciones >= 2 AND b.duration_seconds >= 900
          AND b.after_a >= 1 AND b.after_b >= 1
    ) AS eligible_after
FROM bunching_speed_backfill b
JOIN vialis.recorridos r
    ON r.gtfs_route_id = b.gtfs_route_id AND r.direction_id = b.gtfs_direction_id
WHERE r.id_recorrido = 1714
GROUP BY r.id_recorrido, r.linea, r.ramal;

\if :apply
    WITH updated AS (
        UPDATE vialis.aglutinaciones_colectivos e
        SET
            velocidad_maxima_a_metros_segundo = GREATEST(e.velocidad_maxima_a_metros_segundo, b.after_a),
            velocidad_maxima_b_metros_segundo = GREATEST(e.velocidad_maxima_b_metros_segundo, b.after_b)
        FROM bunching_speed_backfill b
        WHERE e.id_evento = b.id_evento
          AND (b.after_a > e.velocidad_maxima_a_metros_segundo OR b.after_b > e.velocidad_maxima_b_metros_segundo)
        RETURNING e.id_evento
    )
    SELECT count(*) AS events_updated FROM updated;
    COMMIT;
\else
    ROLLBACK;
\endif
