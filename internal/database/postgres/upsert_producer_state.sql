INSERT INTO vialis.h0012_estados_productor (modelo, vehiculo, checkpoint, estado)
VALUES ($1, $2, $3, $4)
ON CONFLICT (modelo, vehiculo)
DO UPDATE SET checkpoint = EXCLUDED.checkpoint, estado = EXCLUDED.estado
