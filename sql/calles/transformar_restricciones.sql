\set ON_ERROR_STOP on

-- Traduce las relaciones OSM type=restriction de vialis.calles_restricciones_raw
-- a giros prohibidos entre aristas de vialis.calles. Corre después de
-- transformar_calles.sql, porque las aristas y los vértices de la carga activa
-- son los que la tabla referencia.
--
-- Reglas, documentadas en sql/calles/README.md:
--
-- - El valor que aplica a un colectivo sale de restriction:bus; si no está y
--   except incluye bus o psv, la relación no aplica; si no, de
--   restriction:motor_vehicle y, por último, de restriction. Las relaciones que
--   sólo restringen otros vehículos (restriction:hgv, restriction:bicycle) o
--   sólo tienen restriction:conditional quedan sin valor y no aplican.
-- - Sólo se traducen relaciones con exactamente un way from, un nodo via y un
--   way to. Las que usan ways como via quedan afuera.
-- - La arista desde es la del way from que puede llegar al vértice via; la
--   arista hacia, la del way to que puede salir de él. Si hay más de una
--   candidata, la relación es ambigua y se descarta en lugar de adivinar.
-- - no_* prohíbe el par (desde, hacia). only_* prohíbe todas las otras salidas
--   del vértice via desde esa arista, incluida la vuelta en U.

BEGIN;

CREATE TEMP TABLE restricciones_traducibles ON COMMIT DROP AS
WITH relaciones AS (
    SELECT
        raw.osm_relation_id,
        raw.miembros,
        CASE
            WHEN raw.etiquetas ? 'restriction:bus'
            THEN raw.etiquetas ->> 'restriction:bus'
            WHEN regexp_split_to_array(
                    lower(COALESCE(raw.etiquetas ->> 'except', '')),
                    '\s*;\s*'
                 ) && ARRAY['bus', 'psv']
            THEN NULL
            WHEN raw.etiquetas ? 'restriction:motor_vehicle'
            THEN raw.etiquetas ->> 'restriction:motor_vehicle'
            ELSE raw.etiquetas ->> 'restriction'
        END AS restriccion
    FROM vialis.calles_restricciones_raw raw
    WHERE raw.etiquetas ->> 'type' = 'restriction'
), miembros AS (
    SELECT
        relacion.osm_relation_id,
        relacion.restriccion,
        array_agg((miembro ->> 'ref')::bigint) FILTER (
            WHERE miembro ->> 'tipo' = 'way' AND miembro ->> 'rol' = 'from'
        ) AS ways_desde,
        array_agg((miembro ->> 'ref')::bigint) FILTER (
            WHERE miembro ->> 'rol' = 'via'
        ) AS vias,
        array_agg((miembro ->> 'ref')::bigint) FILTER (
            WHERE miembro ->> 'tipo' = 'node' AND miembro ->> 'rol' = 'via'
        ) AS nodos_via,
        array_agg((miembro ->> 'ref')::bigint) FILTER (
            WHERE miembro ->> 'tipo' = 'way' AND miembro ->> 'rol' = 'to'
        ) AS ways_hacia
    FROM relaciones relacion
    CROSS JOIN LATERAL jsonb_array_elements(relacion.miembros) miembro
    WHERE relacion.restriccion ~ '^(no|only)_'
    GROUP BY relacion.osm_relation_id, relacion.restriccion
)
SELECT
    miembros.osm_relation_id,
    miembros.restriccion,
    vertice.id_vertice,
    entradas.aristas AS entradas,
    salidas.aristas AS salidas
FROM miembros
JOIN vialis.calles_vertices vertice ON vertice.osm_node_id = miembros.nodos_via[1]
CROSS JOIN LATERAL (
    SELECT array_agg(calle.id_calle ORDER BY calle.id_calle) AS aristas
    FROM vialis.calles calle
    WHERE calle.osm_way_id = miembros.ways_desde[1]
      AND (
          calle.destino = vertice.id_vertice
          OR calle.origen = vertice.id_vertice AND calle.costo_inverso > 0
      )
) entradas
CROSS JOIN LATERAL (
    SELECT array_agg(calle.id_calle ORDER BY calle.id_calle) AS aristas
    FROM vialis.calles calle
    WHERE calle.osm_way_id = miembros.ways_hacia[1]
      AND (
          calle.origen = vertice.id_vertice
          OR calle.destino = vertice.id_vertice AND calle.costo_inverso > 0
      )
) salidas
WHERE cardinality(miembros.ways_desde) = 1
  AND cardinality(miembros.vias) = 1
  AND cardinality(miembros.nodos_via) = 1
  AND cardinality(miembros.ways_hacia) = 1
  AND cardinality(entradas.aristas) = 1
  AND cardinality(salidas.aristas) = 1;

TRUNCATE TABLE vialis.calles_restricciones;

INSERT INTO vialis.calles_restricciones (
    osm_relation_id, restriccion, id_vertice_via, id_calle_desde, id_calle_hacia
)
SELECT
    restriccion.osm_relation_id,
    restriccion.restriccion,
    restriccion.id_vertice,
    restriccion.entradas[1],
    restriccion.salidas[1]
FROM restricciones_traducibles restriccion
WHERE restriccion.restriccion LIKE 'no\_%'
UNION ALL
SELECT
    restriccion.osm_relation_id,
    restriccion.restriccion,
    restriccion.id_vertice,
    restriccion.entradas[1],
    salida.id_calle
FROM restricciones_traducibles restriccion
JOIN vialis.calles salida
  ON salida.origen = restriccion.id_vertice
  OR salida.destino = restriccion.id_vertice AND salida.costo_inverso > 0
WHERE restriccion.restriccion LIKE 'only\_%'
  AND salida.id_calle <> restriccion.salidas[1]
ORDER BY 1, 4, 5;

COMMIT;
