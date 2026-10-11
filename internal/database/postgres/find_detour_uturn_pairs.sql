-- Pares que la pasada principal (find_detour_paths.sql) descartó por un giro en U
-- Y cuyo camino pasa por un PUNTO DE PASO AJENO (un via-point que no es extremo de
-- ese par). Son los únicos candidatos a re-rutear en la segunda pasada.
--
-- El filtro de giro en U cumple doble función en la llamada bulk: descarta U-turns
-- reales/artefactos, y de paso fuerza el servicio de un solo lado (el sentido que
-- no corresponde al lado 'r' sale como reversa y se cae). Por eso NO alcanza con
-- "fue descartado por giro en U": hay que distinguir.
--   * Si la reversa la causa un punto AJENO que partió una arista en el camino de
--     este par (p.ej. una parada opcional que el desvío igual descarta), es un
--     artefacto de contaminación entre pares: re-rutear aislado lo limpia. SÍ.
--   * Si no hay ningún punto ajeno en el camino, la reversa la causan los propios
--     extremos del par (es el servicio de un solo lado): re-rutearlo lo rompería. NO.
--
-- `node` de pgr_trsp_withPoints es negativo en los puntos de paso: node = start_vid
-- en el extremo origen, node = end_vid en el destino; cualquier otro node < 0 es un
-- punto ajeno. Devuelve los pares (pid positivos) a reintentar.
WITH routed AS (
    SELECT *
    FROM pgr_trsp_withPoints(
        'SELECT id, source, target, cost, reverse_cost FROM detour_graph ORDER BY id',
        'SELECT ARRAY[from_edge, to_edge] AS path, ''Infinity''::float8 AS cost FROM detour_restrictions ORDER BY from_edge, to_edge',
        'SELECT pid, edge_id, fraction, side FROM detour_points ORDER BY pid',
        'SELECT source, target FROM detour_combinations ORDER BY source, target',
        'r',
        directed => true,
        details => false
    )
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
        step.node,
        step.edge,
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
)
-- Pares con un giro en U cuya vuelta NO ocurre sobre un extremo propio del par.
-- `turn_node` es el nodo donde el camino se da vuelta (nodo inicial del paso de
-- vuelta). Si ese nodo es el origen o el destino del par (start_vid/end_vid), la
-- reversa la causan los propios extremos: es el servicio de un solo lado ('r'),
-- NO se reintenta (re-rutearlo serviría la parada del lado equivocado). Si la
-- vuelta es en cualquier otro lado —un punto de paso ajeno (una parada opcional
-- que el desvío igual descarta) o un vértice de calle que ese punto partió—, es
-- contaminación entre pares de la llamada bulk: re-rutear aislado la limpia. SÍ.
SELECT DISTINCT -start_vid AS from_point, -end_vid AS to_point
FROM (
    SELECT
        start_vid,
        end_vid,
        node AS turn_node,
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
  AND turn_node <> start_vid
  AND turn_node <> end_vid
ORDER BY from_point, to_point;
