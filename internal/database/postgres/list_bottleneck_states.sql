SELECT
    trazado,
    vehiculo,
    checkpoint,
    estado
FROM vialis.h0012_estados
WHERE modelo = $1
ORDER BY trazado,
vehiculo
