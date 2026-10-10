CREATE INDEX ON bunching_snapshot USING GIST ((posicion::geography));
CREATE UNIQUE INDEX ON bunching_snapshot (vehiculo);
ANALYZE bunching_snapshot;
