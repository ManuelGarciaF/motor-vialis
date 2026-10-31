-- Un renglon por recorrido que pasa el filtro, con la metadata que necesita un
-- listado y el total de coincidencias para que el cliente arme su paginador sin
-- recorrerlas todas.
--
-- $1 texto de busqueda ('' = sin filtro, comodines ya escapados)
-- $2..$5 caja geografica en WGS 84 (NULL = sin filtro)
-- $6 limite, $7 desplazamiento
WITH conteo_paradas AS (
    SELECT
        rp.id_recorrido,
        COUNT(*)::INTEGER AS cantidad_paradas
    FROM vialis.recorridos_paradas rp
    GROUP BY rp.id_recorrido
), filtrados AS (
    SELECT
        r.id_recorrido,
        r.linea,
        r.ramal,
        r.nombre_publico,
        r.direction_id,
        COALESCE(r.destino, '') AS destino,
        COALESCE(r.descripcion, '') AS descripcion,
        r.distancia_metros,
        COALESCE(cp.cantidad_paradas, 0) AS cantidad_paradas,
        -- Relevancia de la coincidencia: quien busca "59" espera la 59 antes
        -- que la 159. 0 = la linea o el nombre publico son el texto buscado,
        -- 1 = empiezan con el, 2 = lo contienen, 3 = solo coincide el destino.
        -- $1 ya viene con los comodines escapados, asi que ILIKE $1 sin % es
        -- una igualdad sin distinguir mayusculas.
        CASE
            WHEN $1::TEXT = '' THEN 0
            WHEN r.linea ILIKE $1::TEXT OR r.nombre_publico ILIKE $1::TEXT THEN 0
            WHEN r.linea ILIKE $1::TEXT || '%' OR r.nombre_publico ILIKE $1::TEXT || '%' THEN 1
            WHEN r.linea ILIKE '%' || $1::TEXT || '%'
                OR r.ramal ILIKE '%' || $1::TEXT || '%'
                OR r.nombre_publico ILIKE '%' || $1::TEXT || '%' THEN 2
            ELSE 3
        END AS relevancia,
        -- Orden natural: la 2 antes que la 10 y la 59 antes que la 159. Las
        -- lineas sin numero (NORTE, SUR) van al final.
        SUBSTRING(r.linea FROM '^[0-9]+')::INTEGER AS numero_linea
    FROM vialis.recorridos r
    LEFT JOIN conteo_paradas cp
        ON cp.id_recorrido = r.id_recorrido
    WHERE (
        $1::TEXT = ''
        OR r.linea ILIKE '%' || $1::TEXT || '%'
        OR r.ramal ILIKE '%' || $1::TEXT || '%'
        OR r.nombre_publico ILIKE '%' || $1::TEXT || '%'
        OR COALESCE(r.destino, '') ILIKE '%' || $1::TEXT || '%'
    )
    -- El operador && usa el indice GIST sobre geom: alcanza con que la caja
    -- del recorrido toque la del viewport, no hace falta interseccion exacta
    -- para decidir que mostrar en un mapa.
    AND (
        $2::DOUBLE PRECISION IS NULL
        OR r.geom && ST_MakeEnvelope(
            $2::DOUBLE PRECISION,
            $3::DOUBLE PRECISION,
            $4::DOUBLE PRECISION,
            $5::DOUBLE PRECISION,
            4326
        )
    )
)
SELECT
    id_recorrido,
    linea,
    ramal,
    nombre_publico,
    direction_id,
    destino,
    descripcion,
    distancia_metros,
    cantidad_paradas,
    COUNT(*) OVER ()::INTEGER AS total
FROM filtrados
-- Orden explicito y total: dos paginas consecutivas de la misma consulta tienen
-- que ser dos ventanas de la misma lista, no dos muestras distintas.
ORDER BY relevancia, numero_linea NULLS LAST, linea, ramal, direction_id, id_recorrido
LIMIT $6::INTEGER
OFFSET $7::INTEGER;
