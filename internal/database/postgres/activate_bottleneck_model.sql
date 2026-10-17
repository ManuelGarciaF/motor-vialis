INSERT INTO vialis.h0012_modelos (
    nombre,
    configuracion,
    captura_inicial,
    checkpoint
)
SELECT
    $1,
    $2,
    coalesce(max(id_captura), 0),
    coalesce(max(id_captura), 0)
FROM vialis.capturas_colectivos
