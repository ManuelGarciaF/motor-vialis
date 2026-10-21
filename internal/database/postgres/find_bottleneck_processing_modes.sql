SELECT
    EXISTS (SELECT 1 FROM vialis.h0012_productores WHERE modelo = $1),
    EXISTS (SELECT 1 FROM vialis.h0012_estados WHERE modelo = $1)
