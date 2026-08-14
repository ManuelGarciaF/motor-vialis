-- Recrea la tabla de staging del CSV de viajes.
--
-- Es descartable, igual que las tablas `gtfs_*_raw`: se elimina y se vuelve a
-- crear en cada ingesta completa. Se mantiene UNLOGGED y sin índices ni
-- constraints para que el COPY sea lo más rápido posible; las columnas
-- conservan los nombres del archivo de origen.
\set ON_ERROR_STOP on

DROP TABLE IF EXISTS vialis.viajes_raw;

CREATE UNLOGGED TABLE vialis.viajes_raw (
    id_tarjeta                 BIGINT,
    id_viaje                   SMALLINT,
    cantidad_etapas            SMALLINT,
    rango_horario              SMALLINT,

    etapas_subte               SMALLINT,
    etapas_tren                SMALLINT,
    etapas_colectivo           SMALLINT,

    longitud_origen_viaje      DOUBLE PRECISION,
    latitud_origen_viaje       DOUBLE PRECISION,

    longitud_destino_viaje     DOUBLE PRECISION,
    latitud_destino_viaje      DOUBLE PRECISION,

    departamento_origen_viaje  CHAR(5),
    departamento_destino_viaje CHAR(5),

    factor_expansion_viaje     REAL,

    etapas_incompletas         CHAR(1),
    genero                     CHAR(1),
    grupo_edad                 SMALLINT
);
