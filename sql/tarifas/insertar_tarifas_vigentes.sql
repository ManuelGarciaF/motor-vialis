-- Cuadro tarifario AMBA publicado para agosto de 2026.
-- Fuente: https://www.argentina.gob.ar/redsube/tarifas-de-transporte-publico-amba
-- Los límites superiores son exclusivos: 0-3 km se representa como [0, 3000).
INSERT INTO vialis.tarifas_colectivo (
    jurisdiccion,
    distancia_min_metros,
    distancia_max_metros,
    tarifa_registrada_centavos,
    tarifa_sin_registrar_centavos
) VALUES
    ('caba',     0,  3000,  85290, 135611),
    ('caba',  3000,  6000,  94771, 150686),
    ('caba',  6000, 12000, 102071, 162293),
    ('caba', 12000, 27000, 109377, 173909),
    ('province',     0,  3000, 111119, 222238),
    ('province',  3000,  6000, 125008, 250016),
    ('province',  6000, 12000, 138898, 277796),
    ('province', 12000, 27000, 166678, 333356),
    ('province', 27000,  NULL, 195958, 391916),
    ('national',     0,  3000,  74281, 148562),
    ('national',  3000,  6000,  86166, 172332),
    ('national',  6000, 12000, 100280, 200560),
    ('national', 12000, 27000, 115136, 230272),
    ('national', 27000,  NULL, 133706, 267412);
