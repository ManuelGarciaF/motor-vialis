INSERT INTO vialis.h0012_estados (
    modelo,
    trazado,
    vehiculo,
    checkpoint,
    estado
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5
)
ON CONFLICT (
    modelo,
    trazado,
    vehiculo
)
DO UPDATE
SET
    checkpoint = EXCLUDED.checkpoint,
    estado = EXCLUDED.estado
