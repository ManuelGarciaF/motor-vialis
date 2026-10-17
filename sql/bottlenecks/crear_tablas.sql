-- Persistencia experimental H0012. No activa procesamiento ni lee el histórico.
-- Las identidades no referencian tablas GTFS ni capturas sujetas a purga.
CREATE TABLE IF NOT EXISTS vialis.h0012_modelos (
    nombre TEXT PRIMARY KEY CHECK (btrim(nombre) <> ''),
    configuracion JSONB NOT NULL CHECK (jsonb_typeof(configuracion) = 'object'),
    activado_en TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    captura_inicial BIGINT NOT NULL CHECK (captura_inicial >= 0),
    checkpoint BIGINT NOT NULL CHECK (checkpoint >= captura_inicial),
    ultima_captura_recibida_en TIMESTAMPTZ,
    CHECK ((checkpoint = captura_inicial AND ultima_captura_recibida_en IS NULL)
        OR (checkpoint > captura_inicial AND ultima_captura_recibida_en IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS vialis.h0012_trazados (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    modelo TEXT NOT NULL REFERENCES vialis.h0012_modelos(nombre),
    gtfs_route_id TEXT NOT NULL,
    direction_id SMALLINT NOT NULL CHECK (direction_id IN (0,1)),
    gtfs_shape_id TEXT NOT NULL,
    gtfs_shape_hash TEXT NOT NULL,
    linea TEXT NOT NULL,
    ramal TEXT NOT NULL,
    longitud_metros DOUBLE PRECISION NOT NULL CHECK (longitud_metros > 0 AND longitud_metros < 'Infinity'::float8),
    geom GEOMETRY(LineString,4326) NOT NULL CHECK (NOT ST_IsEmpty(geom) AND ST_IsValid(geom)),
    paradas JSONB NOT NULL CHECK (jsonb_typeof(paradas) = 'array'),
    UNIQUE (modelo, gtfs_route_id, direction_id, gtfs_shape_id, gtfs_shape_hash),
    UNIQUE (modelo, id)
);

CREATE TABLE IF NOT EXISTS vialis.h0012_sectores (
    trazado BIGINT NOT NULL REFERENCES vialis.h0012_trazados(id),
    indice INTEGER NOT NULL CHECK (indice >= 0),
    inicio_metros DOUBLE PRECISION NOT NULL CHECK (inicio_metros >= 0),
    fin_metros DOUBLE PRECISION NOT NULL CHECK (fin_metros > inicio_metros AND fin_metros < 'Infinity'::float8),
    geom GEOMETRY(LineString,4326) NOT NULL CHECK (NOT ST_IsEmpty(geom) AND ST_IsValid(geom)),
    PRIMARY KEY (trazado, indice)
);
CREATE INDEX IF NOT EXISTS idx_h0012_sectores_geom ON vialis.h0012_sectores USING GIST (geom);

-- El JSON conserva timestamps submicrosegundo y el estado derivado mínimo,
-- no coordenadas GPS ni listas de observaciones históricas.
CREATE TABLE IF NOT EXISTS vialis.h0012_estados (
    modelo TEXT NOT NULL,
    trazado BIGINT NOT NULL,
    vehiculo TEXT NOT NULL CHECK (btrim(vehiculo) <> ''),
    checkpoint BIGINT NOT NULL CHECK (checkpoint > 0),
    estado JSONB NOT NULL CHECK (jsonb_typeof(estado) = 'object'),
    PRIMARY KEY (modelo, trazado, vehiculo),
    FOREIGN KEY (modelo, trazado) REFERENCES vialis.h0012_trazados(modelo, id)
);

-- Completos e incompletos comparten identidad: una entrada soportada no puede
-- publicarse simultáneamente como pasaje válido y como cierre incompleto.
CREATE TABLE IF NOT EXISTS vialis.h0012_mediciones (
    modelo TEXT NOT NULL,
    identidad TEXT NOT NULL,
    trazado BIGINT NOT NULL,
    sector INTEGER NOT NULL,
    vehiculo TEXT NOT NULL CHECK (btrim(vehiculo) <> ''),
    gcba_route_id TEXT NOT NULL,
    gcba_direction SMALLINT NOT NULL CHECK (gcba_direction IN (0,1)),
    captura BIGINT NOT NULL CHECK (captura > 0),
    entrada_en TIMESTAMPTZ NOT NULL,
    fecha_local DATE NOT NULL,
    hora_local SMALLINT NOT NULL CHECK (hora_local BETWEEN 0 AND 23),
    completa BOOLEAN NOT NULL,
    motivo TEXT,
    duracion_ns BIGINT,
    velocidad_kph DOUBLE PRECISION,
    soporte JSONB NOT NULL CHECK (jsonb_typeof(soporte) = 'object'),
    PRIMARY KEY (modelo, identidad),
    FOREIGN KEY (modelo, trazado) REFERENCES vialis.h0012_trazados(modelo, id),
    FOREIGN KEY (trazado, sector) REFERENCES vialis.h0012_sectores(trazado, indice),
    CHECK ((completa AND motivo IS NULL AND duracion_ns > 0 AND duracion_ns IS NOT NULL
            AND velocidad_kph > 0 AND velocidad_kph < 'Infinity'::float8 AND velocidad_kph IS NOT NULL)
        OR (NOT completa AND motivo IS NOT NULL AND duracion_ns IS NULL AND velocidad_kph IS NULL))
);
CREATE INDEX IF NOT EXISTS idx_h0012_mediciones_sector_hora
ON vialis.h0012_mediciones (modelo, trazado, sector, fecha_local, hora_local);

-- Diagnósticos por captura: no duplican GPS ni sustituyen entradas soportadas.
CREATE TABLE IF NOT EXISTS vialis.h0012_diagnosticos (
    modelo TEXT NOT NULL REFERENCES vialis.h0012_modelos(nombre),
    captura BIGINT NOT NULL CHECK (captura > 0),
    diagnostico JSONB NOT NULL CHECK (jsonb_typeof(diagnostico) = 'object'),
    PRIMARY KEY (modelo, captura)
);
