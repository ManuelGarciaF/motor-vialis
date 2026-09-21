-- Transforma el staging `viajes_raw` en `vialis.viajes`: convierte las
-- coordenadas en puntos PostGIS, crea los índices espaciales y asigna la celda
-- H3 de resolución 8 a cada extremo del viaje.
--
-- Este script asume que el CSV ya fue cargado en `viajes_raw`; esa carga la hace
-- `cmd/initdb`, que es el paso que antes figuraba como pendiente dentro de
-- `importar_viajes.sql`.

-- 1. Actualizar estadísticas de la tabla viajes_raw --
VACUUM ANALYZE vialis.viajes_raw;

-- 2. Insertar datos en la tabla viajes desde viajes_raw (para guardar puntos en vez de longitud y latitud) --
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

    departamento_origen_viaje,
    departamento_destino_viaje,

    factor_expansion_viaje,

    etapas_incompletas,
    genero,
    grupo_edad

)

SELECT

    id_tarjeta,
    id_viaje,
    cantidad_etapas,
    rango_horario,

    etapas_subte,
    etapas_tren,
    etapas_colectivo,

    ST_SetSRID(
        ST_MakePoint(
            longitud_origen_viaje,
            latitud_origen_viaje
        ),
        4326
    ),

    ST_SetSRID(
        ST_MakePoint(
            longitud_destino_viaje,
            latitud_destino_viaje
        ),
        4326
    ),

    departamento_origen_viaje,
    departamento_destino_viaje,

    factor_expansion_viaje,

    etapas_incompletas,
    genero,
    grupo_edad

FROM vialis.viajes_raw;

-- 3. Crear índice espacial en la tabla viajes para geom_origen y geom_destino --
CREATE INDEX idx_viajes_geom_origen
ON vialis.viajes
USING GIST (geom_origen);

CREATE INDEX idx_viajes_geom_destino
ON vialis.viajes
USING GIST (geom_destino);

-- 4. Actualizar estadísticas de la tabla viajes --
VACUUM ANALYZE vialis.viajes;

-- 5. Agregar numeros de celda h3 a cada viaje --
UPDATE vialis.viajes
SET h3_origen = h3_lat_lng_to_cell(geom_origen, 8),
    h3_destino = h3_lat_lng_to_cell(geom_destino, 8);
