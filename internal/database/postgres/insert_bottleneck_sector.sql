INSERT INTO vialis.bottlenecks_sectores (
    trazado,
    indice,
    inicio_metros,
    fin_metros,
    geom
)
SELECT
    id,
    $2,
    $3,
    $4,
    ST_LineSubstring(geom::geography, $5, $6)::geometry
FROM vialis.bottlenecks_trazados
WHERE id = $1
