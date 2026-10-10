-- $1: esquema operacional (vialis en producción).
SELECT
    to_regclass(format('%I.capturas_colectivos', $1::text)) IS NOT NULL
    AND to_regclass(format('%I.posiciones_colectivos_raw', $1::text)) IS NOT NULL
    AND to_regclass(format('%I.equivalencias_recorridos_gcba', $1::text)) IS NOT NULL
    AND to_regclass(format('%I.recorridos', $1::text)) IS NOT NULL
    AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = $1
          AND table_name = 'aglutinaciones_colectivos'
          AND column_name = 'ultima_confirmacion_en'
    )
    AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = $1
          AND table_name = 'aglutinaciones_colectivos'
          AND column_name = 'velocidad_maxima_a_metros_segundo'
    )
    AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = $1
          AND table_name = 'aglutinaciones_colectivos'
          AND column_name = 'velocidad_maxima_b_metros_segundo'
    )
    AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = $1
          AND table_name = 'capturas_colectivos'
          AND column_name = 'hash_posiciones'
    );
