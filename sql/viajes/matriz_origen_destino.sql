-- Agrega los factores de expansión por par de celdas de origen y destino.
--
-- La matriz se reemplaza por completo: es una agregación derivada de
-- `vialis.viajes`, de modo que acumular cargas sucesivas sumaría dos veces los
-- mismos viajes. El TRUNCATE y el INSERT van en una sola transacción para que
-- la matriz nunca quede vacía si el INSERT falla.
\set ON_ERROR_STOP on

BEGIN;

TRUNCATE TABLE vialis.matriz_origen_destino;

INSERT INTO vialis.matriz_origen_destino (
    h3_origen,
    h3_destino,
    cantidad_viajes
)
SELECT
    h3_origen,
    h3_destino,
    SUM(factor_expansion_viaje) AS cantidad_viajes
FROM vialis.viajes
WHERE h3_origen IS NOT NULL
  AND h3_destino IS NOT NULL
GROUP BY
    h3_origen,
    h3_destino;

COMMIT;

ANALYZE vialis.matriz_origen_destino;

SELECT COUNT(*) AS pares_origen_destino
FROM vialis.matriz_origen_destino;
