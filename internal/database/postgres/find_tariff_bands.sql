SELECT
    distancia_min_metros,
    distancia_max_metros,
    tarifa_registrada_centavos,
    tarifa_sin_registrar_centavos
FROM vialis.tarifas_colectivo
WHERE jurisdiccion = $1
ORDER BY distancia_min_metros;
