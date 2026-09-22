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

-- Las zonas que une cada combinacion, y la tabla de flujos que reemplazan.
--
-- `combinaciones_lineas_flujos` guardaba los tres pares origen-destino mas
-- grandes de cada combinacion. Era una muestra enganosa: el mayor explica entre
-- el 5 y el 23 % del volumen de su combinacion y los tres juntos entre el 14 y
-- el 56 %, asi que el detalle de la fila mostraba una quinta parte del total
-- como si fuera el todo. La reemplazan cuatro columnas en la propia
-- combinacion, con la celda dominante de cada lado.
--
-- Vacia el agregado: los valores nuevos se llenan al repoblarlo con
-- sql/viajes/combinaciones_lineas.sql.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'vialis'
          AND table_name = 'combinaciones_lineas'
          AND column_name = 'h3_origen_dominante'
    ) THEN
        TRUNCATE vialis.combinaciones_lineas;
        ALTER TABLE vialis.combinaciones_lineas
            ADD COLUMN h3_origen_dominante H3INDEX NOT NULL
                REFERENCES vialis.hexagonos_viajes(indice_h3),
            ADD COLUMN h3_destino_dominante H3INDEX NOT NULL
                REFERENCES vialis.hexagonos_viajes(indice_h3),
            ADD COLUMN nombre_origen TEXT NOT NULL,
            ADD COLUMN nombre_destino TEXT NOT NULL,
            ADD COLUMN pares_od_distintos INTEGER NOT NULL
                CHECK (pares_od_distintos >= 1);
    END IF;
END
$$;

DROP TABLE IF EXISTS vialis.combinaciones_lineas_flujos;
