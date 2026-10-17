SELECT
    configuracion,
    activado_en,
    captura_inicial,
    checkpoint,
    ultima_captura_recibida_en
FROM vialis.h0012_modelos
WHERE nombre = $1
