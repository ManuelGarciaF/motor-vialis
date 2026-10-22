SELECT vehiculo, checkpoint, estado
FROM vialis.bottlenecks_estados_productor
WHERE modelo = $1
ORDER BY vehiculo
