SELECT vehiculo, checkpoint, estado
FROM vialis.h0012_estados_productor
WHERE modelo = $1
ORDER BY vehiculo
