-- $1..$27: argumentos de queueEvent, en orden estable.
INSERT INTO vialis.aglutinaciones_colectivos (
    gcba_route_id,
    gcba_direction,
    gtfs_route_id,
    gtfs_direction_id,
    gtfs_shape_id,
    gtfs_shape_hash,
    id_vehiculo_a,
    id_vehiculo_b,
    primero_observado_en,
    ultimo_observado_en,
    observado_a_en,
    observado_b_en,
    cerrado_en,
    motivo_cierre,
    distancia_minima_metros,
    ultima_distancia_metros,
    ultima_posicion_a,
    ultima_posicion_b,
    observaciones,
    capturas_faltantes,
    linea,
    ramal,
    ultima_confirmacion_en,
    velocidad_maxima_a_metros_segundo,
    velocidad_maxima_b_metros_segundo
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
    ST_SetSRID(ST_MakePoint($17, $18), 4326),
    ST_SetSRID(ST_MakePoint($19, $20), 4326),
    $21, $22, $23, $24, $25, $26, $27
);
