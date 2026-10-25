-- Segunda pasada del ruteo de desvíos: re-rutea SOLO los pares que la pasada
-- principal (find_detour_paths.sql, una sola llamada con TODOS los puntos)
-- descartó por un giro en U.
--
-- En la llamada bulk, pgr_trsp_withPoints parte las aristas donde cae CUALQUIER
-- punto de paso. Con `driving_side => 'r'` un punto intermedio (p.ej. una parada
-- opcional que el desvío igual descarta) puede obligar al camino de OTRO par a
-- pasar por él y volver para salir por el lado correcto: un ida-y-vuelta de pocos
-- metros que el filtro de giro en U confunde con un U-turn real y hace descartar
-- la conexión entera, perdiendo un desvío legal.
--
-- Acá cada par marcado se rutea aislado, con SOLO sus dos puntos. Sin aristas
-- partidas de más, el camino es el mínimo real. Mantiene `'r'`, así que la
-- semántica de servir la parada de un solo lado se respeta para los extremos del
-- par. Si aun aislado el camino sigue con una reversa, es un giro en U de verdad
-- (restricción de giro que obliga a volver) y se descarta igual.
--
-- $1 = JSON array de pares a reintentar: [{"from": <pid>, "to": <pid>}, ...]
-- (pid positivos; se niegan para armar las combinaciones como detour_combinations).
WITH retry_combinations AS (
    SELECT
        -(pair ->> 'from')::bigint AS source,
        -(pair ->> 'to')::bigint AS target
    FROM jsonb_array_elements($1::jsonb) AS pair
    JOIN detour_points origin ON origin.pid = (pair ->> 'from')::bigint
    JOIN detour_points destination ON destination.pid = (pair ->> 'to')::bigint
    WHERE (pair ->> 'from')::bigint <> (pair ->> 'to')::bigint
), routed AS (
    SELECT r.*
    FROM retry_combinations combo
    CROSS JOIN LATERAL pgr_trsp_withPoints(
        'SELECT id, source, target, cost, reverse_cost FROM detour_graph ORDER BY id',
        'SELECT ARRAY[from_edge, to_edge] AS path, ''Infinity''::float8 AS cost FROM detour_restrictions ORDER BY from_edge, to_edge',
        format(
            'SELECT pid, edge_id, fraction, side FROM detour_points WHERE pid IN (%s, %s) ORDER BY pid',
            -combo.source, -combo.target
        ),
        format('SELECT %s::bigint AS source, %s::bigint AS target', combo.source, combo.target),
        'r',
        directed => true,
        -- Igual que la pasada principal: sin details el camino lista sólo los
        -- vértices y sus propios extremos, que es todo lo que necesita la geometría.
        details => false
    ) AS r
), sequenced AS (
    SELECT
        routed.*,
        lead(node) OVER (
            PARTITION BY start_vid, end_vid
            ORDER BY path_seq
        ) AS next_node
    FROM routed
), traversed AS (
    SELECT
        step.start_vid,
        step.end_vid,
        step.path_seq,
        step.edge,
        step.cost,
        step.agg_cost,
        graph.geom,
        CASE
            WHEN step.node < 0 THEN current_point.fraction
            WHEN step.node = graph.source THEN 0::double precision
            WHEN step.node = graph.target THEN 1::double precision
        END AS start_fraction,
        CASE
            WHEN step.next_node < 0 THEN next_point.fraction
            WHEN step.next_node = graph.source THEN 0::double precision
            WHEN step.next_node = graph.target THEN 1::double precision
        END AS end_fraction
    FROM sequenced step
    JOIN detour_graph graph ON graph.id = step.edge
    LEFT JOIN detour_points current_point ON current_point.pid = abs(step.node)
    LEFT JOIN detour_points next_point ON next_point.pid = abs(step.next_node)
    WHERE step.edge <> -1
), giros_en_u AS (
    -- Un par aislado que todavía recorre la misma arista en sentidos opuestos en
    -- dos pasos consecutivos es un giro en U real (lo fuerza una restricción de
    -- giro), no un artefacto de contaminación: se descarta igual que en la pasada
    -- principal.
    SELECT DISTINCT start_vid, end_vid
    FROM (
        SELECT
            start_vid,
            end_vid,
            edge,
            sign(end_fraction - start_fraction) AS sentido,
            lag(edge) OVER w AS arista_previa,
            lag(sign(end_fraction - start_fraction)) OVER w AS sentido_previo
        FROM traversed
        WHERE start_fraction IS NOT NULL
          AND end_fraction IS NOT NULL
          AND start_fraction <> end_fraction
        WINDOW w AS (PARTITION BY start_vid, end_vid ORDER BY path_seq)
    ) consecutivos
    WHERE edge = arista_previa
      AND sentido <> sentido_previo
), pieces AS (
    SELECT
        start_vid,
        end_vid,
        path_seq,
        edge,
        cost,
        agg_cost,
        CASE
            WHEN start_fraction <= end_fraction THEN ST_LineSubstring(
                geom,
                start_fraction,
                end_fraction
            )
            ELSE ST_Reverse(ST_LineSubstring(
                geom,
                end_fraction,
                start_fraction
            ))
        END AS geom
    FROM traversed
    WHERE start_fraction IS NOT NULL
      AND end_fraction IS NOT NULL
      AND start_fraction <> end_fraction
), rumbos AS (
    -- Igual que en la pasada principal (find_detour_paths.sql): sin esto, el
    -- reintento podría devolver un giro en U que la principal ya descartó.
    SELECT
        piece.start_vid,
        piece.end_vid,
        row_number() OVER (
            PARTITION BY piece.start_vid, piece.end_vid
            ORDER BY piece.path_seq
        ) AS orden,
        measured.largo,
        coalesce(street.junction_osm IN ('roundabout', 'circular'), false)
            OR street.tipo LIKE '%\_link' AS admite_vuelta,
        CASE WHEN measured.largo >= 5 THEN ST_Azimuth(
            ST_StartPoint(piece.geom)::geography,
            ST_LineInterpolatePoint(piece.geom, least(1, 15 / measured.largo))::geography
        ) END AS rumbo_salida,
        CASE WHEN measured.largo >= 5 THEN ST_Azimuth(
            ST_LineInterpolatePoint(piece.geom, greatest(0, 1 - 15 / measured.largo))::geography,
            ST_EndPoint(piece.geom)::geography
        ) END AS rumbo_llegada
    FROM pieces piece
    CROSS JOIN LATERAL (SELECT ST_Length(piece.geom::geography) AS largo) measured
    JOIN vialis.calles street ON street.id_calle = piece.edge
), giros_cruzando_calzada AS (
    SELECT DISTINCT antes.start_vid, antes.end_vid
    FROM rumbos antes
    JOIN rumbos despues
      ON despues.start_vid = antes.start_vid
     AND despues.end_vid = antes.end_vid
     AND despues.orden BETWEEN antes.orden + 1 AND antes.orden + 6
    CROSS JOIN LATERAL (
        SELECT
            coalesce(sum(medio.largo), 0) AS largo,
            coalesce(bool_or(medio.admite_vuelta), false) AS admite_vuelta
        FROM rumbos medio
        WHERE medio.start_vid = antes.start_vid
          AND medio.end_vid = antes.end_vid
          AND medio.orden > antes.orden
          AND medio.orden < despues.orden
    ) medio
    WHERE antes.rumbo_llegada IS NOT NULL
      AND despues.rumbo_salida IS NOT NULL
      AND medio.largo <= 30
      AND NOT (antes.admite_vuelta OR despues.admite_vuelta OR medio.admite_vuelta)
      AND degrees(pi() - abs(pi() - abs(antes.rumbo_llegada - despues.rumbo_salida))) >= 150
), paths AS (
    SELECT
        -start_vid AS from_point,
        -end_vid AS to_point,
        max(agg_cost + cost) AS travel_seconds,
        array_agg(edge ORDER BY path_seq) AS edge_ids,
        ST_MakeLine(geom ORDER BY path_seq) AS geom
    FROM pieces
    WHERE NOT EXISTS (
        SELECT 1 FROM giros_en_u g
        WHERE g.start_vid = pieces.start_vid
          AND g.end_vid = pieces.end_vid
    )
      AND NOT EXISTS (
        SELECT 1 FROM giros_cruzando_calzada g
        WHERE g.start_vid = pieces.start_vid
          AND g.end_vid = pieces.end_vid
    )
    GROUP BY start_vid, end_vid
), connected AS (
    SELECT
        paths.from_point,
        paths.to_point,
        paths.travel_seconds,
        paths.edge_ids,
        ST_MakeLine(ARRAY[
            ST_MakeLine(origin.requested_geom, origin.snapped_geom),
            paths.geom,
            ST_MakeLine(destination.snapped_geom, destination.requested_geom)
        ]) AS geom
    FROM paths
    JOIN detour_points origin ON origin.pid = paths.from_point
    JOIN detour_points destination ON destination.pid = paths.to_point
)
SELECT
    connected.from_point,
    connected.to_point,
    connected.travel_seconds,
    connected.edge_ids,
    ST_AsGeoJSON(connected.geom)::bytea
FROM connected
CROSS JOIN detour_context context
-- See snap_detour_points.sql: this only absorbs boundary round-off.
WHERE ST_Length(ST_Difference(
        connected.geom,
        context.search_area
      )::geography) <= 0.01
  AND NOT ST_Intersects(connected.geom, context.forbidden_area)
ORDER BY connected.from_point, connected.to_point;
