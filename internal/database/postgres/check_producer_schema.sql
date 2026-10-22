-- $1: application schema. Readiness is independent of model activation.
SELECT
    to_regclass(format('%I.bottlenecks_modelos', $1::text)) IS NOT NULL
    AND to_regclass(format('%I.bottlenecks_trazados', $1::text)) IS NOT NULL
    AND to_regclass(format('%I.bottlenecks_mediciones', $1::text)) IS NOT NULL
    AND to_regclass(format('%I.bottlenecks_sectores', $1::text)) IS NOT NULL
    AND to_regclass(format('%I.posiciones_colectivos_raw', $1::text)) IS NOT NULL
    AND to_regclass(format('%I.bottlenecks_estados_productor', $1::text)) IS NOT NULL
    AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = $1 AND table_name = 'capturas_colectivos'
          AND column_name = 'filas_insertadas'
    )
