-- $1: recibido_en; $2..$6: conteos total/válidas/inválidas/duplicadas/stale.
-- $7: cobertura parcial.
INSERT INTO vialis.capturas_colectivos (
    recibido_en,
    filas_totales,
    filas_validas,
    filas_invalidas,
    filas_duplicadas,
    filas_stale,
    cobertura_parcial
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id_captura;
