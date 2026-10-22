-- One statement gives sectors and source checkpoint the same MVCC snapshot.
-- The period is half-open and assigned by estimated entry, not closure time.
WITH candidates AS (
    SELECT s.*, t.gtfs_route_id, t.gtfs_shape_hash, t.linea, t.ramal, t.direction_id
    FROM vialis.bottlenecks_sectores s
    JOIN vialis.bottlenecks_trazados t ON t.id = s.trazado
    WHERE t.modelo = $1
      AND ($6::text = '' OR t.linea = $6)
      AND (NOT $7::boolean OR ST_Intersects(s.geom, ST_MakeEnvelope($8, $9, $10, $11, 4326)))
), metrics AS (
    SELECT m.trazado, m.sector,
           count(*) FILTER (WHERE m.completa) AS complete_passages,
           count(*) FILTER (WHERE NOT m.completa) AS incomplete_passages,
           count(*) FILTER (WHERE m.completa AND m.velocidad_kph < $4) AS slow_passages,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY m.velocidad_kph)
               FILTER (WHERE m.completa) AS median_speed,
           max(m.entrada_en) AS last_entry
    FROM vialis.bottlenecks_mediciones m
    JOIN candidates c ON c.trazado = m.trazado AND c.indice = m.sector
    WHERE m.modelo = $1 AND m.entrada_en >= $2 AND m.entrada_en < $3
    GROUP BY m.trazado, m.sector
    HAVING count(*) FILTER (WHERE m.completa AND m.velocidad_kph < $4) >= $5
)
SELECT jsonb_build_object(
    'source', jsonb_build_object(
        'initialized', EXISTS (SELECT 1 FROM vialis.bottlenecks_modelos WHERE nombre = $1),
        'checkpoint', coalesce((SELECT checkpoint FROM vialis.bottlenecks_modelos WHERE nombre = $1), 0),
        'lastCaptureReceivedAt', (SELECT ultima_captura_recibida_en FROM vialis.bottlenecks_modelos WHERE nombre = $1)
    ),
    'sectors', coalesce((SELECT jsonb_agg(jsonb_build_object(
        'layoutId', c.trazado, 'sector', c.indice,
        'gtfsRouteId', c.gtfs_route_id, 'shapeHash', c.gtfs_shape_hash,
        'line', c.linea, 'branch', c.ramal, 'direction', c.direction_id,
        'geometry', ST_AsGeoJSON(c.geom, 6)::jsonb,
        'lengthMeters', c.fin_metros - c.inicio_metros,
        'routeEnd', c.indice = 0 OR NOT EXISTS (
            SELECT 1 FROM vialis.bottlenecks_sectores next
            WHERE next.trazado = c.trazado AND next.indice > c.indice
        ),
        'completePassages', m.complete_passages, 'incompletePassages', m.incomplete_passages,
        'slowPassages', m.slow_passages,
        'slowPercentage', 100.0 * m.slow_passages / m.complete_passages,
        'medianSpeedKph', m.median_speed, 'lastEntryAt', m.last_entry
    ) ORDER BY c.gtfs_route_id, c.direction_id, c.gtfs_shape_hash, c.trazado, c.indice)
    FROM metrics m JOIN candidates c ON c.trazado = m.trazado AND c.indice = m.sector), '[]'::jsonb)
);
