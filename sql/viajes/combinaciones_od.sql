-- Puebla vialis.combinaciones_od: cuantos viajes con trasbordo van de una celda
-- H3 a otra, en cada banda horaria.
--
-- Puede volver a ejecutarse: vacia la tabla antes de recalcularla.
--
-- QUE CUENTA COMO COMBINACION
--
-- `cantidad_etapas > 1`. La fuente no tiene una tabla de etapas ni una marca de
-- trasbordo: el unico indicio de que la persona tuvo que combinar es que su
-- viaje se compuso de mas de una etapa.
--
-- `etapas_colectivo = cantidad_etapas` deja solo los viajes que fueron
-- integramente en colectivo. El feed GTFS cargado es exclusivamente de
-- colectivos, de modo que un viaje que combino colectivo con subte o tren
-- produciria itinerarios de colectivo para un trasbordo que nunca ocurrio entre
-- colectivos.
--
-- FACTOR DE EXPANSION
--
-- Se suma el factor, no las filas: contar filas describe la muestra, sumar el
-- factor estima los viajes representados. Un factor no informado aporta cero,
-- igual que en hexagonos_viajes.sql y matriz_origen_destino.sql, y por la misma
-- razon practica: en el CSV del dia tipico hay filas sin factor, y sin el
-- COALESCE un par de celdas compuesto solo por ellas produciria un SUM nulo
-- contra el NOT NULL de viajes_estimados.
TRUNCATE vialis.combinaciones_od;

INSERT INTO vialis.combinaciones_od (
    h3_origen,
    h3_destino,
    rango_horario,
    viajes_estimados
)
SELECT
    h3_origen,
    h3_destino,
    rango_horario,
    COALESCE(SUM(factor_expansion_viaje), 0) AS viajes_estimados
FROM vialis.viajes
WHERE cantidad_etapas > 1
  AND etapas_colectivo = cantidad_etapas
  AND h3_origen IS NOT NULL
  AND h3_destino IS NOT NULL
  AND rango_horario IS NOT NULL
GROUP BY
    h3_origen,
    h3_destino,
    rango_horario;

VACUUM ANALYZE vialis.combinaciones_od;
