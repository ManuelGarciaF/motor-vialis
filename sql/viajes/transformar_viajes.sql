-- Transforma el staging de viajes en el modelo final.
--
-- Convierte las coordenadas en puntos PostGIS, asigna la celda H3 de
-- resolución 8 de cada extremo y reemplaza por completo el contenido de
-- `vialis.viajes`. El reemplazo es total y no incremental: la carga representa
-- un día hábil típico, no una serie histórica que se acumule.
\set ON_ERROR_STOP on

ANALYZE vialis.viajes_raw;

BEGIN;

TRUNCATE TABLE vialis.viajes;

-- El índice H3 se calcula dentro del INSERT en lugar de con un UPDATE
-- posterior: un UPDATE reescribiría todas las filas recién insertadas y
-- duplicaría el tamaño de la tabla hasta el siguiente VACUUM.
INSERT INTO vialis.viajes (
    id_tarjeta,
    id_viaje,
    cantidad_etapas,
    rango_horario,

    etapas_subte,
    etapas_tren,
    etapas_colectivo,

    geom_origen,
    geom_destino,

    h3_origen,
    h3_destino,

    departamento_origen_viaje,
    departamento_destino_viaje,

    factor_expansion_viaje,

    etapas_incompletas,
    genero,
    grupo_edad
)
SELECT
    viaje.id_tarjeta,
    viaje.id_viaje,
    viaje.cantidad_etapas,
    viaje.rango_horario,

    viaje.etapas_subte,
    viaje.etapas_tren,
    viaje.etapas_colectivo,

    punto.origen,
    punto.destino,

    h3_lat_lng_to_cell(punto.origen, 8),
    h3_lat_lng_to_cell(punto.destino, 8),

    viaje.departamento_origen_viaje,
    viaje.departamento_destino_viaje,

    viaje.factor_expansion_viaje,

    viaje.etapas_incompletas,
    viaje.genero,
    viaje.grupo_edad
FROM vialis.viajes_raw viaje
CROSS JOIN LATERAL (
    SELECT
        ST_SetSRID(
            ST_MakePoint(
                viaje.longitud_origen_viaje,
                viaje.latitud_origen_viaje
            ),
            4326
        ) AS origen,
        ST_SetSRID(
            ST_MakePoint(
                viaje.longitud_destino_viaje,
                viaje.latitud_destino_viaje
            ),
            4326
        ) AS destino
) punto;

COMMIT;

-- Los índices espaciales se crean después de la carga para no penalizar el
-- INSERT masivo.
CREATE INDEX IF NOT EXISTS idx_viajes_geom_origen
ON vialis.viajes
USING GIST (geom_origen);

CREATE INDEX IF NOT EXISTS idx_viajes_geom_destino
ON vialis.viajes
USING GIST (geom_destino);

VACUUM ANALYZE vialis.viajes;

SELECT
    COUNT(*)                                        AS viajes,
    COUNT(*) FILTER (WHERE h3_origen IS NOT NULL)   AS con_h3_origen,
    COUNT(*) FILTER (WHERE h3_destino IS NOT NULL)  AS con_h3_destino
FROM vialis.viajes;
