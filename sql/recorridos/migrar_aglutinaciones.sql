\set ON_ERROR_STOP on
BEGIN;
-- Conservador para capturas históricas: no reconstruir snapshots completos
-- desde raw deduplicado. Sólo las nuevas capturas podrán analizarse.
ALTER TABLE vialis.capturas_colectivos
 ADD COLUMN IF NOT EXISTS cobertura_parcial BOOLEAN NOT NULL DEFAULT TRUE,
 ADD COLUMN IF NOT EXISTS aglutinaciones_analizadas BOOLEAN NOT NULL DEFAULT FALSE,
 ADD COLUMN IF NOT EXISTS hash_posiciones TEXT NOT NULL DEFAULT '';
ALTER TABLE vialis.aglutinaciones_colectivos
 ADD COLUMN IF NOT EXISTS linea TEXT NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS ramal TEXT NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS gcba_route_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS gcba_direction SMALLINT NOT NULL DEFAULT 0 CHECK (gcba_direction IN (0,1)),
 -- No backfill: pre-existing encounters remain below the publication threshold.
 ADD COLUMN IF NOT EXISTS velocidad_maxima_a_metros_segundo DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (velocidad_maxima_a_metros_segundo >= 0 AND velocidad_maxima_a_metros_segundo < 'Infinity'::DOUBLE PRECISION),
 ADD COLUMN IF NOT EXISTS velocidad_maxima_b_metros_segundo DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (velocidad_maxima_b_metros_segundo >= 0 AND velocidad_maxima_b_metros_segundo < 'Infinity'::DOUBLE PRECISION),
 ADD COLUMN IF NOT EXISTS observado_a_en TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS observado_b_en TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS ultima_confirmacion_en TIMESTAMPTZ,
 ADD COLUMN IF NOT EXISTS capturas_faltantes INTEGER NOT NULL DEFAULT 0 CHECK (capturas_faltantes>=0),
 ADD COLUMN IF NOT EXISTS motivo_cierre TEXT CHECK (motivo_cierre IN ('separated','missing','expired','identity_changed'));
DROP INDEX IF EXISTS vialis.idx_aglutinaciones_colectivos_abiertas;
CREATE UNIQUE INDEX idx_aglutinaciones_colectivos_abiertas
ON vialis.aglutinaciones_colectivos (gcba_route_id,gcba_direction,gtfs_route_id,gtfs_direction_id,gtfs_shape_hash,id_vehiculo_a,id_vehiculo_b)
WHERE cerrado_en IS NULL;
CREATE INDEX IF NOT EXISTS idx_aglutinaciones_colectivos_historia
ON vialis.aglutinaciones_colectivos ((COALESCE(cerrado_en,ultimo_observado_en)),primero_observado_en DESC,id_evento DESC);
-- Inicio de evidencia; complementa el índice por última observación para
-- intersectar la ventana semanal, independientemente del cierre.
CREATE INDEX IF NOT EXISTS idx_aglutinaciones_colectivos_inicio
ON vialis.aglutinaciones_colectivos (primero_observado_en,gtfs_route_id,gtfs_direction_id);
COMMIT;
