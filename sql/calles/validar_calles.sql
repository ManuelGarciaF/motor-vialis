\set ON_ERROR_STOP on

DO $$
DECLARE
    errores text[] := ARRAY[]::text[];
BEGIN
    IF NOT EXISTS (SELECT 1 FROM vialis.calles) THEN
        errores := array_append(errores, 'vialis.calles está vacía');
    END IF;
    IF EXISTS (
        SELECT 1 FROM vialis.calles
        WHERE costo <= 0 OR (costo_inverso <= 0 AND costo_inverso <> -1)
    ) THEN
        errores := array_append(errores, 'hay costos inválidos');
    END IF;
    IF EXISTS (
        SELECT 1 FROM vialis.calles
        WHERE NOT ST_IsValid(geom) OR ST_IsEmpty(geom) OR ST_NPoints(geom) < 2
    ) THEN
        errores := array_append(errores, 'hay geometrías inválidas o degeneradas');
    END IF;
    IF EXISTS (
        SELECT 1 FROM vialis.calles c
        LEFT JOIN vialis.calles_vertices o ON o.id_vertice = c.origen
        LEFT JOIN vialis.calles_vertices d ON d.id_vertice = c.destino
        WHERE o.id_vertice IS NULL OR d.id_vertice IS NULL
    ) THEN
        errores := array_append(errores, 'hay aristas con vértices inexistentes');
    END IF;
    IF cardinality(errores) > 0 THEN
        RAISE EXCEPTION 'Validación estructural fallida: %', array_to_string(errores, '; ');
    END IF;
END
$$;

SELECT
    count(*) AS aristas,
    count(*) FILTER (WHERE costo_inverso = -1) AS aristas_sentido_unico,
    count(DISTINCT osm_way_id) AS ways_osm,
    round(sum(costo)::numeric / 1000, 1) AS kilometros
FROM vialis.calles;

SELECT count(*) AS vertices FROM vialis.calles_vertices;

WITH carga AS (
    SELECT alcance FROM vialis.calles_metadata WHERE activa
), cercania AS (
    SELECT p.id_parada,
           EXISTS (
               SELECT 1
               FROM vialis.calles c
               WHERE c.geom && ST_Expand(p.posicion, 0.0006)
                 AND ST_DWithin(p.posicion::geography, c.geom::geography, 50)
           ) AS cubierta
    FROM vialis.paradas p, carga c
    WHERE ST_Covers(c.alcance, p.posicion)
)
SELECT
    count(*) AS paradas_en_alcance,
    count(*) FILTER (WHERE cubierta) AS paradas_con_calle_a_50m,
    round(100 * count(*) FILTER (WHERE cubierta)::numeric / NULLIF(count(*), 0), 2)
        AS porcentaje_cubierto
FROM cercania;

SELECT
    id_carga, fuente_url, fecha_datos, cantidad_aristas, cantidad_vertices,
    round((100 * proporcion_descartada)::numeric, 3) AS porcentaje_descartado
FROM vialis.calles_metadata
WHERE activa;
