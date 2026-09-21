-- 1. Insertar datos en la tabla de matriz de viajes --
--
-- Un factor de expansion no informado aporta cero, igual que en
-- hexagonos_viajes.sql. Sin el COALESCE, un par de celdas cuyos viajes no
-- tengan ningun factor produce un SUM nulo y la carga se cae contra el NOT NULL
-- de cantidad_viajes: en el CSV del dia tipico hay filas sin factor.
INSERT INTO vialis.matriz_origen_destino (
    h3_origen,
    h3_destino,
    cantidad_viajes
)
SELECT
    h3_origen,
    h3_destino,
    COALESCE(SUM(factor_expansion_viaje), 0) AS cantidad_viajes
FROM vialis.viajes
WHERE h3_origen IS NOT NULL
  AND h3_destino IS NOT NULL
GROUP BY
    h3_origen,
    h3_destino;

-- TODO: ver si es necesario crear índices --
