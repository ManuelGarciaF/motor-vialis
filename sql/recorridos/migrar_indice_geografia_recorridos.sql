\set ON_ERROR_STOP on

BEGIN;

-- Agrega el índice de expresión en geography que vialis.recorridos incorporó en
-- ddl.sql para la búsqueda de corredores detrás de POST /lines/similar. Una
-- base creada antes de que existiera ese endpoint ya tiene idx_recorridos_geom,
-- que indexa la columna geom pelada y por eso es inutilizable por un ST_DWithin
-- en geography: la búsqueda igual respondería, pero recorriendo y bufereando
-- cada recorrido del feed. Ejecutar esto sobre una base existente en lugar de
-- rehacerla.
--
-- No recalcula nada ni cambia datos, así que después no hace falta volver a
-- ejecutar transformar_gtfs.sql.
CREATE INDEX IF NOT EXISTS idx_recorridos_geom_geography
ON vialis.recorridos
USING GIST ((geom::geography));

COMMIT;
