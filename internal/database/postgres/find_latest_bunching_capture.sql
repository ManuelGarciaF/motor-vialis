SELECT recibido_en, cobertura_parcial
FROM vialis.capturas_colectivos
WHERE aglutinaciones_analizadas
ORDER BY recibido_en DESC, id_captura DESC
LIMIT 1;
