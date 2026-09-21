-- Tabla de staging del CSV de viajes.
--
-- Sus columnas siguen el orden exacto del encabezado de
-- `viajes_BAdata_20241016.csv`, porque `cmd/initdb` la carga con COPY en ese
-- mismo orden. Cambiar el orden acá obliga a cambiarlo en
-- `internal/database/bootstrap`.
CREATE TABLE vialis.viajes_raw (

    id_tarjeta BIGINT,
    id_viaje SMALLINT,
    cantidad_etapas SMALLINT,
    rango_horario SMALLINT,

    etapas_subte SMALLINT,
    etapas_tren SMALLINT,
    etapas_colectivo SMALLINT,

    longitud_origen_viaje DOUBLE PRECISION,
    latitud_origen_viaje DOUBLE PRECISION,

    longitud_destino_viaje DOUBLE PRECISION,
    latitud_destino_viaje DOUBLE PRECISION,

    departamento_origen_viaje CHAR(5),
    departamento_destino_viaje CHAR(5),

    factor_expansion_viaje REAL,

    etapas_incompletas CHAR(1),
    genero CHAR(1),
    grupo_edad SMALLINT
);
