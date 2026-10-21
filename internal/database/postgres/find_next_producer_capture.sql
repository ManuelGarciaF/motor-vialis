SELECT id_captura, recibido_en, cobertura_parcial, filas_insertadas
FROM vialis.capturas_colectivos
WHERE id_captura > $1
ORDER BY id_captura
LIMIT 1
FOR SHARE
