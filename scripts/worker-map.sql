-- Snapshot de diagnóstico; sólo lectura. Variables psql numéricas validadas por
-- worker-map.mjs: hours y row_limit. No exporta IDs de vehículos ni GPS raw.
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '15s';
SET LOCAL lock_timeout = '1s';
WITH events AS (
    SELECT id_evento, linea, ramal, gtfs_direction_id AS sentido,
           primero_observado_en AS inicio, ultimo_observado_en AS ultima_observacion,
           cerrado_en AS cierre, motivo_cierre, observaciones,
           extract(epoch FROM ultimo_observado_en - primero_observado_en) AS duracion_segundos,
           round(ultima_distancia_metros::numeric, 1) AS distancia_metros,
           ST_Centroid(ST_Collect(ultima_posicion_a, ultima_posicion_b)) AS geom
    FROM vialis.aglutinaciones_colectivos
    WHERE ultimo_observado_en >= now() - (:hours * interval '1 hour')
    ORDER BY ultimo_observado_en DESC, id_evento DESC
    LIMIT :row_limit + 1
), sectors AS (
    SELECT trazado, sector,
           count(*) FILTER (WHERE completa) AS pasajes,
           count(*) FILTER (WHERE NOT completa) AS incompletos,
           count(*) FILTER (WHERE completa AND velocidad_kph < 6) AS pasajes_lentos,
           round((percentile_cont(0.5) WITHIN GROUP (ORDER BY duracion_ns / 60000000000.0)
                  FILTER (WHERE completa))::numeric, 2) AS duracion_mediana_min,
           round((percentile_cont(0.5) WITHIN GROUP (ORDER BY velocidad_kph)
                  FILTER (WHERE completa))::numeric, 1) AS velocidad_mediana_kph,
           max(entrada_en) AS ultima_entrada
    FROM vialis.bottlenecks_mediciones
    WHERE entrada_en >= now() - (:hours * interval '1 hour')
    GROUP BY trazado, sector
    HAVING count(*) FILTER (WHERE completa AND velocidad_kph < 6) > 0
    ORDER BY max(entrada_en) DESC, trazado, sector
)
SELECT json_build_object(
    'generated_at', now(),
    'hours', :hours,
    'limit', :row_limit,
    'slow_threshold_kph', 6,
    'recurrent_min_passages', 2,
    'status', json_build_object(
        'capture', (SELECT json_build_object('id', id_captura, 'received_at', recibido_en,
                       'valid', filas_validas, 'inserted', filas_insertadas,
                       'partial', cobertura_parcial)
                    FROM vialis.capturas_colectivos ORDER BY id_captura DESC LIMIT 1),
        'processors', (SELECT coalesce(json_agg(json_build_object(
            'checkpoint', p.checkpoint, 'initialized_at', p.activado_en,
            'received_at', p.ultima_captura_recibida_en,
            'pending_captures', (SELECT count(*) FROM vialis.capturas_colectivos c
                                  WHERE c.id_captura > p.checkpoint)
        ) ORDER BY p.nombre), '[]'::json) FROM vialis.bottlenecks_modelos p),
        'states', (SELECT count(*) FROM vialis.bottlenecks_estados_productor),
        'open_sequences', (SELECT count(*) FROM vialis.bottlenecks_estados_productor
                           WHERE estado->'Open' IS NOT NULL AND estado->'Open' <> 'null'::jsonb)
    ),
    'events', json_build_object('type', 'FeatureCollection', 'features',
        (SELECT coalesce(json_agg(json_build_object(
            'type', 'Feature', 'geometry', ST_AsGeoJSON(e.geom, 6)::json,
            'properties', to_jsonb(e) - 'geom'
        ) ORDER BY e.ultima_observacion DESC, e.id_evento DESC), '[]'::json) FROM events e)),
    'sectors', json_build_object('type', 'FeatureCollection', 'features',
        (SELECT coalesce(json_agg(json_build_object(
            'type', 'Feature', 'geometry', ST_AsGeoJSON(s.geom, 6)::json,
            'properties', to_jsonb(m) || jsonb_build_object(
                'linea', t.linea, 'ramal', t.ramal, 'sentido', t.direction_id,
                'longitud_metros', round((s.fin_metros - s.inicio_metros)::numeric, 1),
                'extremo_recorrido', s.indice = 0 OR NOT EXISTS (
                    SELECT 1 FROM vialis.bottlenecks_sectores siguiente
                    WHERE siguiente.trazado = s.trazado AND siguiente.indice > s.indice
                )
            )
        ) ORDER BY m.ultima_entrada DESC, m.trazado, m.sector), '[]'::json)
         FROM sectors m
         JOIN vialis.bottlenecks_sectores s ON s.trazado=m.trazado AND s.indice=m.sector
         JOIN vialis.bottlenecks_trazados t ON t.id=m.trazado))
);
COMMIT;
