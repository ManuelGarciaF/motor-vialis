\set ON_ERROR_STOP on

-- Agrega a una base existente el mapping muchos-a-uno entre identidades de la
-- API realtime de GCBA y recorridos GTFS. Los datos se cargan por separado con
-- cargar_equivalencias_gcba.sql.

CREATE TABLE IF NOT EXISTS vialis.equivalencias_recorridos_gcba (
    gcba_route_id                   TEXT NOT NULL
        CHECK (BTRIM(gcba_route_id) <> ''),
    gcba_direction                  SMALLINT NOT NULL
        CHECK (gcba_direction IN (0, 1)),
    gtfs_route_id                   TEXT NOT NULL
        CHECK (BTRIM(gtfs_route_id) <> ''),
    gtfs_direction_id               SMALLINT NOT NULL
        CHECK (gtfs_direction_id IN (0, 1)),
    PRIMARY KEY (gcba_route_id, gcba_direction)
);
