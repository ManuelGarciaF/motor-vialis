\set ON_ERROR_STOP on

BEGIN;

ALTER TABLE vialis.recorridos_paradas
    ADD COLUMN IF NOT EXISTS tiempo_valle_hasta_siguiente_segundos INTEGER,
    ADD COLUMN IF NOT EXISTS tiempo_tipico_hasta_siguiente_segundos INTEGER,
    ADD COLUMN IF NOT EXISTS tiempo_pico_hasta_siguiente_segundos INTEGER,
    ADD COLUMN IF NOT EXISTS cantidad_muestras_tiempo INTEGER
        NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'vialis.recorridos_paradas'::regclass
            AND conname = 'recorridos_paradas_tiempo_valle_positivo'
    ) THEN
        ALTER TABLE vialis.recorridos_paradas
            ADD CONSTRAINT recorridos_paradas_tiempo_valle_positivo
            CHECK (
                tiempo_valle_hasta_siguiente_segundos IS NULL
                OR tiempo_valle_hasta_siguiente_segundos > 0
            );
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'vialis.recorridos_paradas'::regclass
            AND conname = 'recorridos_paradas_tiempo_tipico_positivo'
    ) THEN
        ALTER TABLE vialis.recorridos_paradas
            ADD CONSTRAINT recorridos_paradas_tiempo_tipico_positivo
            CHECK (
                tiempo_tipico_hasta_siguiente_segundos IS NULL
                OR tiempo_tipico_hasta_siguiente_segundos > 0
            );
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'vialis.recorridos_paradas'::regclass
            AND conname = 'recorridos_paradas_tiempo_pico_positivo'
    ) THEN
        ALTER TABLE vialis.recorridos_paradas
            ADD CONSTRAINT recorridos_paradas_tiempo_pico_positivo
            CHECK (
                tiempo_pico_hasta_siguiente_segundos IS NULL
                OR tiempo_pico_hasta_siguiente_segundos > 0
            );
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'vialis.recorridos_paradas'::regclass
            AND conname = 'recorridos_paradas_muestras_tiempo_validas'
    ) THEN
        ALTER TABLE vialis.recorridos_paradas
            ADD CONSTRAINT recorridos_paradas_muestras_tiempo_validas
            CHECK (
                (
                    tiempo_valle_hasta_siguiente_segundos IS NULL
                    AND tiempo_tipico_hasta_siguiente_segundos IS NULL
                    AND tiempo_pico_hasta_siguiente_segundos IS NULL
                    AND cantidad_muestras_tiempo = 0
                )
                OR (
                    tiempo_valle_hasta_siguiente_segundos IS NOT NULL
                    AND tiempo_tipico_hasta_siguiente_segundos IS NOT NULL
                    AND tiempo_pico_hasta_siguiente_segundos IS NOT NULL
                    AND tiempo_valle_hasta_siguiente_segundos
                        <= tiempo_tipico_hasta_siguiente_segundos
                    AND tiempo_tipico_hasta_siguiente_segundos
                        <= tiempo_pico_hasta_siguiente_segundos
                    AND cantidad_muestras_tiempo > 0
                )
            );
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_recorridos_paradas_tramo_geography
ON vialis.recorridos_paradas
USING GIST ((tramo_hasta_siguiente::geography))
WHERE tramo_hasta_siguiente IS NOT NULL;

COMMIT;

-- La migración solamente prepara el esquema. Volver a ejecutar
-- transformar_gtfs.sql para calcular y poblar los percentiles.
