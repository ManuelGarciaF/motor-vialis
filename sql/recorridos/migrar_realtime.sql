\set ON_ERROR_STOP on

-- Sólo esquema operacional; no modifica GTFS, viajes ni equivalencias.
BEGIN;

-- Capturas operacionales; posiciones sólo para identidades con equivalencia.
CREATE TABLE IF NOT EXISTS vialis.capturas_colectivos (
    id_captura BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    recibido_en TIMESTAMPTZ NOT NULL,
    filas_totales INTEGER NOT NULL CHECK (filas_totales >= 0),
    filas_validas INTEGER NOT NULL CHECK (filas_validas >= 0),
    filas_invalidas INTEGER NOT NULL CHECK (filas_invalidas >= 0),
    filas_duplicadas INTEGER NOT NULL CHECK (filas_duplicadas >= 0),
    filas_stale INTEGER NOT NULL CHECK (filas_stale >= 0),
    filas_no_vinculadas INTEGER NOT NULL DEFAULT 0 CHECK (filas_no_vinculadas >= 0),
    filas_insertadas INTEGER NOT NULL DEFAULT 0 CHECK (filas_insertadas >= 0),
    cobertura_parcial BOOLEAN NOT NULL DEFAULT TRUE,
    aglutinaciones_analizadas BOOLEAN NOT NULL DEFAULT FALSE,
    hash_posiciones TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_capturas_colectivos_recibido ON vialis.capturas_colectivos (recibido_en);

-- Sin FK hacia el catálogo GTFS: una recarga reinicia sus IDs internos. Se
-- conserva el shape observado para no mezclar versiones de un mismo recorrido.
CREATE TABLE IF NOT EXISTS vialis.posiciones_colectivos_raw (
    dia_observado DATE NOT NULL,
    observado_en TIMESTAMPTZ NOT NULL,
    recibido_en TIMESTAMPTZ NOT NULL,
    id_vehiculo TEXT NOT NULL CHECK (BTRIM(id_vehiculo) <> ''),
    gcba_route_id TEXT NOT NULL,
    gcba_direction SMALLINT NOT NULL CHECK (gcba_direction IN (0, 1)),
    gtfs_route_id TEXT NOT NULL,
    gtfs_direction_id SMALLINT NOT NULL CHECK (gtfs_direction_id IN (0, 1)),
    gtfs_shape_id TEXT NOT NULL,
    gtfs_shape_hash TEXT NOT NULL,
    posicion GEOMETRY(Point, 4326) NOT NULL,
    velocidad_metros_segundo DOUBLE PRECISION NOT NULL CHECK (velocidad_metros_segundo >= 0 AND velocidad_metros_segundo < 'Infinity'::DOUBLE PRECISION),
    CHECK (ST_X(posicion) BETWEEN -180 AND 180 AND ST_Y(posicion) BETWEEN -90 AND 90),
    id_captura BIGINT NOT NULL REFERENCES vialis.capturas_colectivos(id_captura),
    CONSTRAINT posiciones_colectivos_dia_utc CHECK (
        dia_observado = (observado_en AT TIME ZONE 'UTC')::DATE
    ),
    PRIMARY KEY (dia_observado, id_vehiculo, observado_en)
) PARTITION BY RANGE (dia_observado);
CREATE INDEX IF NOT EXISTS idx_posiciones_colectivos_recorrido
ON vialis.posiciones_colectivos_raw (gtfs_route_id, gtfs_direction_id, observado_en);
CREATE INDEX IF NOT EXISTS idx_posiciones_colectivos_geography
ON vialis.posiciones_colectivos_raw USING GIST ((posicion::geography));
CREATE INDEX IF NOT EXISTS idx_posiciones_colectivos_captura ON vialis.posiciones_colectivos_raw (id_captura);

-- Su ciclo de vida se implementa en fase 4. El par canónico evita A-B/B-A.
CREATE TABLE IF NOT EXISTS vialis.aglutinaciones_colectivos (
    id_evento BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    linea TEXT NOT NULL DEFAULT '',
    ramal TEXT NOT NULL DEFAULT '',
    gcba_route_id TEXT NOT NULL DEFAULT '',
    gcba_direction SMALLINT NOT NULL DEFAULT 0 CHECK (gcba_direction IN (0, 1)),
    observado_a_en TIMESTAMPTZ,
    observado_b_en TIMESTAMPTZ,
    ultima_confirmacion_en TIMESTAMPTZ,
    capturas_faltantes INTEGER NOT NULL DEFAULT 0 CHECK (capturas_faltantes >= 0),
    motivo_cierre TEXT CHECK (motivo_cierre IN ('separated', 'missing', 'expired', 'identity_changed')),
    gtfs_route_id TEXT NOT NULL,
    gtfs_direction_id SMALLINT NOT NULL CHECK (gtfs_direction_id IN (0, 1)),
    gtfs_shape_id TEXT NOT NULL,
    gtfs_shape_hash TEXT NOT NULL,
    id_vehiculo_a TEXT NOT NULL,
    id_vehiculo_b TEXT NOT NULL,
    primero_observado_en TIMESTAMPTZ NOT NULL,
    ultimo_observado_en TIMESTAMPTZ NOT NULL,
    cerrado_en TIMESTAMPTZ,
    distancia_minima_metros DOUBLE PRECISION NOT NULL CHECK (distancia_minima_metros >= 0),
    ultima_distancia_metros DOUBLE PRECISION NOT NULL CHECK (ultima_distancia_metros >= 0),
    ultima_posicion_a GEOMETRY(Point, 4326) NOT NULL,
    ultima_posicion_b GEOMETRY(Point, 4326) NOT NULL,
    observaciones INTEGER NOT NULL CHECK (observaciones > 0),
    CHECK (id_vehiculo_a COLLATE "C" < id_vehiculo_b COLLATE "C"),
    CHECK (ultimo_observado_en >= primero_observado_en),
    CHECK (cerrado_en IS NULL OR cerrado_en >= ultimo_observado_en)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_aglutinaciones_colectivos_abiertas
ON vialis.aglutinaciones_colectivos (gcba_route_id, gcba_direction, gtfs_route_id, gtfs_direction_id, gtfs_shape_hash, id_vehiculo_a, id_vehiculo_b)
WHERE cerrado_en IS NULL;
CREATE INDEX IF NOT EXISTS idx_aglutinaciones_colectivos_ultimo ON vialis.aglutinaciones_colectivos (ultimo_observado_en);
CREATE INDEX IF NOT EXISTS idx_aglutinaciones_colectivos_historia
ON vialis.aglutinaciones_colectivos ((COALESCE(cerrado_en,ultimo_observado_en)),primero_observado_en DESC,id_evento DESC);

-- Identidad de la aparición ordenada de parada, sin FK a IDs regenerables.
CREATE TABLE IF NOT EXISTS vialis.pasos_colectivos (
    id_paso BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    gtfs_route_id TEXT NOT NULL,
    gtfs_direction_id SMALLINT NOT NULL CHECK (gtfs_direction_id IN (0, 1)),
    gtfs_shape_id TEXT NOT NULL,
    gtfs_shape_hash TEXT NOT NULL,
    gtfs_stop_id TEXT NOT NULL,
    nro_parada INTEGER NOT NULL CHECK (nro_parada >= 0),
    id_vehiculo TEXT NOT NULL,
    pasado_en TIMESTAMPTZ NOT NULL,
    distancia_metros DOUBLE PRECISION NOT NULL CHECK (distancia_metros >= 0),
    UNIQUE (gtfs_route_id, gtfs_direction_id, gtfs_shape_id, gtfs_shape_hash, nro_parada, id_vehiculo, pasado_en)
);
CREATE INDEX IF NOT EXISTS idx_pasos_colectivos_recorrido_fecha
ON vialis.pasos_colectivos (gtfs_route_id, gtfs_direction_id, pasado_en);

COMMIT;
