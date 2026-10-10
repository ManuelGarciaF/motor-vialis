-- $1: from; $2: to; $3: zona horaria; $4: mínimo de muestras.
-- $5: duración mínima (s); $6: id_recorrido opcional (NULL para resumen global).
-- $7: días distintos mínimos por franja; $8: velocidad máxima mínima por unidad (m/s).
-- Recorta la evidencia a [from,to), sin prolongarla hasta el cierre ni hasta ahora.
WITH eligible AS (
    SELECT
        id_evento,
        gtfs_route_id,
        gtfs_direction_id,
        linea,
        ramal,
        primero_observado_en,
        GREATEST(primero_observado_en, $1) AT TIME ZONE $3 AS local_start,
        LEAST(ultimo_observado_en, $2) AT TIME ZONE $3 AS local_end
    FROM vialis.aglutinaciones_colectivos
    WHERE primero_observado_en < $2
      AND ultimo_observado_en > $1
      AND observaciones >= $4
      AND EXTRACT(EPOCH FROM (ultimo_observado_en - primero_observado_en)) >= $5::double precision
      AND velocidad_maxima_a_metros_segundo >= $8
      AND velocidad_maxima_b_metros_segundo >= $8
      AND (
          $6::bigint IS NULL
          OR EXISTS (
              SELECT 1
              FROM vialis.recorridos r
              WHERE r.id_recorrido = $6
                AND r.gtfs_route_id = aglutinaciones_colectivos.gtfs_route_id
                AND r.direction_id = aglutinaciones_colectivos.gtfs_direction_id
          )
      )
), event_hours AS (
    SELECT
        e.*,
        EXTRACT(HOUR FROM bucket)::integer AS hour,
        to_char(bucket, 'YYYY-MM-DD') AS date
    FROM eligible e
    CROSS JOIN LATERAL generate_series(
        date_trunc('hour', e.local_start),
        date_trunc('hour', e.local_end),
        interval '1 hour'
    ) AS bucket
    -- Un encuentro terminado a las 09:00 no ocupa la franja 09:00–10:00.
    WHERE bucket < e.local_end
), hourly AS (
    SELECT
        gtfs_route_id,
        gtfs_direction_id,
        hour,
        count(DISTINCT id_evento) AS events,
        array_agg(DISTINCT date ORDER BY date) AS dates
    FROM event_hours
    GROUP BY gtfs_route_id, gtfs_direction_id, hour
), hours AS (
    SELECT
        gtfs_route_id,
        gtfs_direction_id,
        jsonb_agg(
            jsonb_build_object(
                'hour', hour,
                'eventCount', events,
                'daysWithEvents', cardinality(dates),
                'dates', dates,
                'recurrent', cardinality(dates) >= $7
            ) ORDER BY hour
        ) AS hours
    FROM hourly
    GROUP BY gtfs_route_id, gtfs_direction_id
), history AS (
    SELECT
        gtfs_route_id,
        gtfs_direction_id,
        count(DISTINCT id_evento) AS events,
        array_agg(DISTINCT date ORDER BY date) AS dates,
        (array_agg(linea ORDER BY primero_observado_en DESC, id_evento DESC))[1] AS linea,
        (array_agg(ramal ORDER BY primero_observado_en DESC, id_evento DESC))[1] AS ramal
    FROM event_hours
    GROUP BY gtfs_route_id, gtfs_direction_id
)
SELECT
    r.id_recorrido,
    COALESCE(r.gtfs_route_id, h.gtfs_route_id),
    COALESCE(r.direction_id, h.gtfs_direction_id),
    COALESCE(r.linea, h.linea, ''),
    COALESCE(r.ramal, h.ramal, ''),
    EXISTS (
        SELECT 1
        FROM vialis.equivalencias_recorridos_gcba m
        WHERE m.gtfs_route_id = r.gtfs_route_id
          AND m.gtfs_direction_id = r.direction_id
    ),
    COALESCE(h.events, 0),
    COALESCE(h.dates, ARRAY[]::text[]),
    COALESCE(hours.hours, '[]'::jsonb)
FROM vialis.recorridos r
FULL JOIN history h
    ON h.gtfs_route_id = r.gtfs_route_id AND h.gtfs_direction_id = r.direction_id
LEFT JOIN hours
    ON hours.gtfs_route_id = h.gtfs_route_id AND hours.gtfs_direction_id = h.gtfs_direction_id
-- El global omite recorridos sin eventos; el individual conserva su resumen vacío.
WHERE ($6::bigint IS NULL AND h.events > 0) OR r.id_recorrido = $6
ORDER BY
    COALESCE(h.events, 0) DESC,
    COALESCE(r.gtfs_route_id, h.gtfs_route_id),
    COALESCE(r.direction_id, h.gtfs_direction_id);
