-- Estado operacional H0012; no activa modelos ni duplica series GPS.
-- La marca de modo existe incluso sin vehículos para impedir mezclar dos
-- procesadores con políticas de confirmación distintas sobre un checkpoint.
CREATE TABLE IF NOT EXISTS vialis.h0012_productores (
    modelo TEXT PRIMARY KEY REFERENCES vialis.h0012_modelos(nombre)
);

CREATE TABLE IF NOT EXISTS vialis.h0012_estados_productor (
    modelo TEXT NOT NULL REFERENCES vialis.h0012_productores(modelo),
    vehiculo TEXT NOT NULL CHECK (btrim(vehiculo) <> ''),
    checkpoint BIGINT NOT NULL CHECK (checkpoint > 0),
    estado JSONB NOT NULL CHECK (jsonb_typeof(estado) = 'object'),
    PRIMARY KEY (modelo, vehiculo)
);
