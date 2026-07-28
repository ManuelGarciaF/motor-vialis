WITH parametros AS (
    SELECT 2044::BIGINT AS id_recorrido
), paradas_ordenadas AS (
    SELECT
        recorrido_parada.nro_parada,
        parada.gtfs_stop_id,
        parada.posicion,
        recorrido_parada.tramo_hasta_siguiente,
        COUNT(*) OVER (
            PARTITION BY parada.gtfs_stop_id
        ) AS cantidad_apariciones
    FROM vialis.recorridos_paradas recorrido_parada
    JOIN vialis.paradas parada
        ON parada.id_parada = recorrido_parada.id_parada
    JOIN parametros
        ON parametros.id_recorrido = recorrido_parada.id_recorrido
), paradas_simulacion AS (
    SELECT
        nro_parada,
        jsonb_build_object(
            'id',
            CASE
                WHEN cantidad_apariciones = 1 THEN gtfs_stop_id
                ELSE concat(gtfs_stop_id, ':', nro_parada)
            END,
            'position',
            jsonb_build_object(
                'latitude', ST_Y(posicion),
                'longitude', ST_X(posicion)
            )
        )
        || CASE
            WHEN tramo_hasta_siguiente IS NOT NULL THEN
                jsonb_build_object(
                    'pathToNext',
                    ST_AsGeoJSON(
                        tramo_hasta_siguiente,
                        9,
                        0
                    )::jsonb
                )
            ELSE '{}'::jsonb
        END AS parada_json
    FROM paradas_ordenadas
)
SELECT jsonb_pretty(
    jsonb_build_object(
        'stops',
        COALESCE(
            jsonb_agg(parada_json ORDER BY nro_parada),
            '[]'::jsonb
        )
    )
) AS ruta_simulacion_json
FROM paradas_simulacion;
