-- One set-based write per capture; vehicle ordering is deterministic. A failure
-- rolls back every row together with final passages and the global checkpoint.
INSERT INTO vialis.bottlenecks_estados_productor (modelo, vehiculo, checkpoint, estado)
SELECT $1, s.estado->>'VehicleID', $2, s.estado
FROM jsonb_array_elements($3::jsonb) AS s(estado)
ORDER BY s.estado->>'VehicleID'
ON CONFLICT (modelo, vehiculo)
DO UPDATE SET checkpoint = EXCLUDED.checkpoint, estado = EXCLUDED.estado;
