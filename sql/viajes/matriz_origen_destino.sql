-- Agrega los factores de expansión por par de celdas H3.
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
