-- Puebla vialis.conexiones_recorridos: que pares de recorridos permiten un
-- trasbordo, y donde.
--
-- Puede volver a ejecutarse: vacia la tabla antes de recalcularla.
--
-- RADIO DE CAMINATA
--
-- 300 metros. Una persona que combina camina hasta la esquina donde para la
-- otra linea, no atraviesa el barrio. El feed del AMBA le da un stop_id propio
-- a cada linea aunque paren en la misma esquina, asi que exigir la misma parada
-- fisica perderia la mayoria de las combinaciones reales; ST_DWithin a 0 metros
-- ya cubre el caso de la parada compartida, que queda incluido aca.
--
-- Este radio es el del ETL. El motor tiene el suyo para decidir que recorridos
-- sirven una celda H3 (config.TransferAccessRadiusMeters) y son preguntas
-- distintas: aquella mide de la celda a la parada, esta de una parada a otra.
--
-- COSTO
--
-- El producto parada x parada dentro del radio es la parte cara. Se calcula una
-- sola vez en `caminatas`, apoyandose en idx_paradas_posicion_geography, y
-- recien despues se expande a los recorridos que pasan por cada punta. Hacerlo
-- al reves multiplicaria el trabajo espacial por la cantidad de recorridos.
--
-- DISTINCT ON deja un unico punto de trasbordo por par ordenado, el de menor
-- caminata. El desempate por identificadores de parada es deterministico a
-- proposito: dos corridas sobre los mismos datos tienen que dar lo mismo.
TRUNCATE vialis.conexiones_recorridos;

WITH caminatas AS (
    SELECT
        bajada.id_parada  AS id_parada_bajada,
        subida.id_parada  AS id_parada_subida,
        ST_Distance(
            bajada.posicion::geography,
            subida.posicion::geography
        ) AS distancia_metros
    FROM vialis.paradas AS bajada
    JOIN vialis.paradas AS subida
      ON ST_DWithin(
             bajada.posicion::geography,
             subida.posicion::geography,
             300
         )
)
INSERT INTO vialis.conexiones_recorridos (
    id_recorrido_origen,
    id_recorrido_destino,
    id_parada_bajada,
    id_parada_subida,
    distancia_caminata_metros
)
SELECT DISTINCT ON (origen.id_recorrido, destino.id_recorrido)
    origen.id_recorrido,
    destino.id_recorrido,
    caminatas.id_parada_bajada,
    caminatas.id_parada_subida,
    ROUND(caminatas.distancia_metros)::INTEGER
FROM caminatas
JOIN vialis.recorridos_paradas AS origen
  ON origen.id_parada = caminatas.id_parada_bajada
JOIN vialis.recorridos_paradas AS destino
  ON destino.id_parada = caminatas.id_parada_subida
WHERE origen.id_recorrido <> destino.id_recorrido
ORDER BY
    origen.id_recorrido,
    destino.id_recorrido,
    caminatas.distancia_metros,
    caminatas.id_parada_bajada,
    caminatas.id_parada_subida;

-- La tabla se lee por las dos puntas en cada consulta de trasbordos y el
-- planificador necesita saber cuantas filas quedaron.
VACUUM ANALYZE vialis.conexiones_recorridos;
