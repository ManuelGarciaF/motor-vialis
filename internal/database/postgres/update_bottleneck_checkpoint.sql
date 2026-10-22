UPDATE vialis.bottlenecks_modelos
SET
    checkpoint = $2,
    ultima_captura_recibida_en = $3
WHERE nombre = $1
