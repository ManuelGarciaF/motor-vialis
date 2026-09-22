\set ON_ERROR_STOP on

-- Agrega a una base YA POBLADA las tablas de H0009 (combinaciones O-D
-- frecuentes). No la ejecuta el inicializador: `cmd/initdb` construye la base
-- desde cero con `ddl.sql`, que ya las incluye.
--
-- Es para la base que existía antes de H0009 y que tiene cargados los
-- recorridos y los viajes: recargarla entera son ocho minutos para obtener
-- exactamente los mismos datos más cuatro tablas vacías.
--
-- Después de este script hay que poblar los agregados, en este orden:
--   psql < sql/recorridos/conexiones_recorridos.sql
--   psql < sql/viajes/combinaciones_od.sql
--   psql < sql/viajes/combinaciones_lineas.sql
--
-- Todo es idempotente: volver a ejecutarlo sobre una base ya migrada no hace
-- nada.

-- El indice GiST de paradas es sobre la columna pelada, en grados. La busqueda
-- de trasbordos mide caminatas en metros con ST_DWithin sobre geography, que no
-- puede usarlo y degradaria a recorrer las 43.594 paradas por cada una.
CREATE INDEX IF NOT EXISTS idx_paradas_posicion_geography
ON vialis.paradas
USING GIST ((posicion::geography));

-- Grafo de trasbordos: que pares de recorridos permiten cambiar de colectivo.
-- Ver sql/recorridos/README.md para el par ordenado y el punto unico.
CREATE TABLE IF NOT EXISTS vialis.conexiones_recorridos (
    id_recorrido_origen        BIGINT NOT NULL
        REFERENCES vialis.recorridos(id_recorrido) ON DELETE CASCADE,
    id_recorrido_destino       BIGINT NOT NULL
        REFERENCES vialis.recorridos(id_recorrido) ON DELETE CASCADE,
    id_parada_bajada           BIGINT NOT NULL
        REFERENCES vialis.paradas(id_parada),
    id_parada_subida           BIGINT NOT NULL
        REFERENCES vialis.paradas(id_parada),
    distancia_caminata_metros  INTEGER NOT NULL
        CHECK (distancia_caminata_metros >= 0),
    CONSTRAINT conexiones_recorridos_distintos
        CHECK (id_recorrido_origen <> id_recorrido_destino),
    PRIMARY KEY (id_recorrido_origen, id_recorrido_destino)
);

CREATE INDEX IF NOT EXISTS idx_conexiones_recorridos_destino
ON vialis.conexiones_recorridos (id_recorrido_destino);

-- Viajes con trasbordo por par de celdas y banda horaria.
CREATE TABLE IF NOT EXISTS vialis.combinaciones_od (
    h3_origen         H3INDEX NOT NULL
        REFERENCES vialis.hexagonos_viajes(indice_h3),
    h3_destino        H3INDEX NOT NULL
        REFERENCES vialis.hexagonos_viajes(indice_h3),
    rango_horario     SMALLINT NOT NULL,
    viajes_estimados  DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (h3_origen, h3_destino, rango_horario)
);

CREATE INDEX IF NOT EXISTS idx_combinaciones_od_banda_horaria
ON vialis.combinaciones_od (rango_horario, viajes_estimados DESC);

-- Ranking de pares de lineas. `rango_horario` NULL es la fila del dia entero.
CREATE TABLE IF NOT EXISTS vialis.combinaciones_lineas (
    id_recorrido_primero   BIGINT NOT NULL
        REFERENCES vialis.recorridos(id_recorrido) ON DELETE CASCADE,
    id_recorrido_segundo   BIGINT NOT NULL
        REFERENCES vialis.recorridos(id_recorrido) ON DELETE CASCADE,
    rango_horario          SMALLINT
        CHECK (rango_horario IS NULL OR rango_horario BETWEEN 0 AND 23),
    viajes_estimados       DOUBLE PRECISION NOT NULL,
    alternativas_promedio  REAL NOT NULL CHECK (alternativas_promedio >= 1),
    rango_horario_pico     SMALLINT NOT NULL
        CHECK (rango_horario_pico BETWEEN 0 AND 23)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_combinaciones_lineas_par
ON vialis.combinaciones_lineas (
    id_recorrido_primero,
    id_recorrido_segundo,
    rango_horario
) NULLS NOT DISTINCT;

CREATE INDEX IF NOT EXISTS idx_combinaciones_lineas_ranking
ON vialis.combinaciones_lineas (rango_horario, viajes_estimados DESC);

-- Los tres flujos mas grandes de cada combinacion, para el detalle de una fila.
CREATE TABLE IF NOT EXISTS vialis.combinaciones_lineas_flujos (
    id_recorrido_primero  BIGINT NOT NULL,
    id_recorrido_segundo  BIGINT NOT NULL,
    posicion              SMALLINT NOT NULL CHECK (posicion >= 1),
    h3_origen             H3INDEX NOT NULL,
    h3_destino            H3INDEX NOT NULL,
    -- Hora en la que el flujo concentra mas viajes, no "la hora del flujo".
    -- Un viaje es su par de celdas: la hora es un atributo suyo y no otra
    -- fila, o el mismo viaje aparece tres veces y nadie puede distinguirlos.
    rango_horario_pico    SMALLINT NOT NULL
        CHECK (rango_horario_pico BETWEEN 0 AND 23),
    viajes_estimados      DOUBLE PRECISION NOT NULL,
    alternativas          INTEGER NOT NULL CHECK (alternativas >= 1),
    -- Nombre de la parada mas cercana a cada celda. Un indice H3 y un par de
    -- coordenadas no le dicen nada a nadie: sin esto, dos flujos que coinciden
    -- en volumen y hora se leen como la misma fila repetida cuando son lugares
    -- distintos. Sale del mismo catalogo GTFS que nombra el punto de
    -- trasbordo, asi que la pantalla habla siempre el mismo idioma.
    nombre_origen         TEXT NOT NULL,
    nombre_destino        TEXT NOT NULL,
    PRIMARY KEY (id_recorrido_primero, id_recorrido_segundo, posicion),
    FOREIGN KEY (id_recorrido_primero)
        REFERENCES vialis.recorridos(id_recorrido) ON DELETE CASCADE,
    FOREIGN KEY (id_recorrido_segundo)
        REFERENCES vialis.recorridos(id_recorrido) ON DELETE CASCADE
);

-- Para una base que ya corrio una version anterior de esta migracion y tiene
-- la tabla sin los nombres. Vacia el agregado: los nombres se llenan al
-- repoblarlo con sql/viajes/combinaciones_lineas.sql.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'vialis'
          AND table_name = 'combinaciones_lineas_flujos'
          AND column_name = 'nombre_origen'
    ) THEN
        TRUNCATE vialis.combinaciones_lineas_flujos;
        ALTER TABLE vialis.combinaciones_lineas_flujos
            ADD COLUMN nombre_origen  TEXT NOT NULL,
            ADD COLUMN nombre_destino TEXT NOT NULL;
    END IF;

    -- La columna paso de ser "la hora del flujo" a "la hora pico del flujo"
    -- cuando los flujos se agruparon por par de celdas en vez de por par y
    -- hora. Los valores viejos no se pueden reinterpretar: se vacia y se
    -- repuebla con combinaciones_lineas.sql.
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'vialis'
          AND table_name = 'combinaciones_lineas_flujos'
          AND column_name = 'rango_horario'
    ) THEN
        TRUNCATE vialis.combinaciones_lineas_flujos;
        ALTER TABLE vialis.combinaciones_lineas_flujos
            RENAME COLUMN rango_horario TO rango_horario_pico;
    END IF;
END
$$;
