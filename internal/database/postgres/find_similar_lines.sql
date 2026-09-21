-- Un renglon por recorrido almacenado que comparte corredor con la ruta
-- dibujada, ordenados de mejor a peor coincidencia.
--
-- La similitud es cobertura mutua de corredores, nunca ST_Intersects: dos
-- trazados sobre la misma avenida, uno dibujado a mano y otro venido de un
-- shape GTFS, practicamente nunca se cruzan en un vertice exacto, asi que una
-- prueba de interseccion informaria como "sin coincidencia" a casi todas las
-- coincidencias reales. En cambio cada linea se mide contra el corredor de la
-- otra, y se devuelven los dos numeros: cuanto de lo que dibujo quien consulta
-- corre dentro del corredor del recorrido almacenado, y cuanto del recorrido
-- almacenado corre dentro del corredor de lo dibujado. No se promedian ni aca
-- ni mas arriba: ver el comentario de lines.Similarity.
--
-- $1 trazado dibujado en GeoJSON
-- $2, $3 longitud y latitud de la primera parada dibujada
-- $4, $5 longitud y latitud de la ultima
-- $6 tolerancia del corredor en metros
-- $7 cobertura minima, 0..1, que tiene que alcanzar al menos un sentido
-- $8 cantidad maxima de renglones
WITH drawn AS (
    SELECT
        ST_SetSRID(
            ST_GeomFromGeoJSON($1::TEXT),
            4326
        )::GEOMETRY(LineString, 4326) AS geom,
        ST_SetSRID(
            ST_MakePoint($2::DOUBLE PRECISION, $3::DOUBLE PRECISION),
            4326
        ) AS origin_point,
        ST_SetSRID(
            ST_MakePoint($4::DOUBLE PRECISION, $5::DOUBLE PRECISION),
            4326
        ) AS destination_point,
        $6::DOUBLE PRECISION AS tolerance_meters
), drawn_measured AS (
    SELECT
        drawn.*,
        ST_Length(drawn.geom::geography) AS length_meters,
        -- Bufferear en geography y volver a geometry mantiene la tolerancia en
        -- metros en toda la consulta. Un buffer tomado en grados seria mas
        -- ancho de norte a sur que de este a oeste, con lo cual "200 m"
        -- significaria dos distancias distintas segun el rumbo.
        ST_Buffer(
            drawn.geom::geography,
            drawn.tolerance_meters
        )::geometry AS corridor
    FROM drawn
), candidates AS (
    -- Prefiltrar por indice antes de medir nada: bufferear e intersecar cada
    -- recorrido del feed del AMBA contra la ruta dibujada costaria mas de lo
    -- que vale la respuesta, y salvo un punado ninguno pasa siquiera cerca.
    --
    -- El casteo a geography es deliberado y tiene un indice detras:
    -- idx_recorridos_geom es un GIST sobre la columna geom pelada, y un
    -- ST_DWithin en geography no puede usarlo, asi que esto degradaria a un
    -- recorrido secuencial con un buffer por renglon. Por eso sql/ddl.sql
    -- declara tambien idx_recorridos_geom_geography sobre (geom::geography),
    -- la misma respuesta con indice de expresion que este esquema ya da por la
    -- misma razon en idx_recorridos_paradas_tramo_geography. La alternativa
    -- -prefiltrar con && contra una caja agrandada en grados- volveria a poner
    -- la tolerancia en grados y reintroduciria la distorsion por latitud que
    -- el buffer de arriba evita.
    SELECT
        r.id_recorrido,
        r.linea,
        r.ramal,
        r.nombre_publico,
        r.direction_id,
        COALESCE(r.destino, '') AS destino,
        COALESCE(r.descripcion, '') AS descripcion,
        r.distancia_metros,
        r.tiempo_total_minutos,
        r.caudal_pasajeros,
        r.ingreso_economico,
        r.geom
    FROM vialis.recorridos r
    CROSS JOIN drawn_measured d
    WHERE ST_DWithin(
        r.geom::geography,
        d.geom::geography,
        d.tolerance_meters
    )
), measured AS (
    SELECT
        c.*,
        stops.stop_count,
        ST_Distance(
            d.origin_point::geography,
            stops.first_stop_position::geography
        ) AS origin_distance_meters,
        ST_Distance(
            d.destination_point::geography,
            stops.last_stop_position::geography
        ) AS destination_distance_meters,
        -- ST_CollectionExtract(..., 2) se queda solo con las partes lineales de
        -- la interseccion: donde un trazado entra a un buffer tambien toca su
        -- borde en un punto, y un punto no aporta nada a un largo pero si
        -- convierte el resultado en una coleccion que ST_Length se negaria a
        -- medir.
        ST_Length(
            ST_CollectionExtract(
                ST_Intersection(
                    d.geom,
                    ST_Buffer(
                        c.geom::geography,
                        d.tolerance_meters
                    )::geometry
                ),
                2
            )::geography
        ) AS drawn_overlap_meters,
        ST_Length(
            ST_CollectionExtract(
                ST_Intersection(c.geom, d.corridor),
                2
            )::geography
        ) AS stored_overlap_meters,
        d.length_meters AS drawn_length_meters,
        ST_Length(c.geom::geography) AS stored_length_meters
    FROM candidates c
    CROSS JOIN drawn_measured d
    JOIN LATERAL (
        SELECT
            COUNT(*)::INTEGER AS stop_count,
            (array_agg(p.posicion ORDER BY rp.nro_parada))[1]
                AS first_stop_position,
            (array_agg(p.posicion ORDER BY rp.nro_parada DESC))[1]
                AS last_stop_position
        FROM vialis.recorridos_paradas rp
        JOIN vialis.paradas p
            ON p.id_parada = rp.id_parada
        WHERE rp.id_recorrido = c.id_recorrido
    ) stops ON stops.stop_count >= 2
    -- El JOIN es interno, y exige dos paradas, a proposito: un recorrido sin
    -- ellas no tiene primera ni ultima parada contra la cual medir las
    -- distancias de los extremos, e informar esas distancias como cero se
    -- leeria como "este recorrido arranca exactamente donde el suyo". Un
    -- recorrido que la ETL dejo sin paradas tampoco se puede simular, asi que
    -- no sirve como baseline.
), coverage AS (
    SELECT
        m.*,
        -- El resguardo no es desconfianza de los datos: una ruta dibujada como
        -- dos paradas coincidentes mide cero metros, y dividir por eso haria
        -- que toda linea de la ciudad se le parezca infinitamente. LEAST acota
        -- el excedente de punto flotante que aparece cuando un trazado queda
        -- entero adentro del buffer del otro y los dos largos se miden por
        -- caminos de calculo distintos.
        CASE
            WHEN m.drawn_length_meters > 0
            THEN LEAST(1, m.drawn_overlap_meters / m.drawn_length_meters)
            ELSE 0
        END AS coverage_of_drawn,
        CASE
            WHEN m.stored_length_meters > 0
            THEN LEAST(
                1,
                m.stored_overlap_meters / m.stored_length_meters
            )
            ELSE 0
        END AS coverage_of_stored
    FROM measured m
)
SELECT
    id_recorrido,
    linea,
    ramal,
    nombre_publico,
    direction_id,
    destino,
    descripcion,
    distancia_metros,
    stop_count,
    coverage_of_drawn,
    coverage_of_stored,
    origin_distance_meters,
    destination_distance_meters,
    -- Se castean para que toda metrica nulable llegue con el mismo ancho; los
    -- NULL pasan intactos, porque un recorrido sin medir no es un recorrido
    -- medido en cero.
    tiempo_total_minutos::BIGINT AS tiempo_total_minutos,
    caudal_pasajeros::BIGINT AS caudal_pasajeros,
    ingreso_economico::BIGINT AS ingreso_economico
FROM coverage
-- Una candidata califica por su sentido mas fuerte: una ruta corta apoyada
-- entera sobre una linea larga cubre casi nada de ella, y esa asimetria es
-- justamente el hallazgo, no una razon para esconder el par.
WHERE GREATEST(coverage_of_drawn, coverage_of_stored) >= $7::DOUBLE PRECISION
-- Se ordena por el sentido mas debil, porque una candidata se parece de verdad
-- solo cuando se parece en los dos: el minimo es la unica lectura del par que
-- un solo sentido no puede inflar, asi que una relacion de fragmento nunca
-- queda por encima de un gemelo genuino. El sentido mas fuerte desempata, y el
-- id resuelve lo que quede para que la misma busqueda devuelva siempre el
-- mismo orden, que es tambien por lo cual el limite se aplica aca y no en Go.
ORDER BY
    LEAST(coverage_of_drawn, coverage_of_stored) DESC,
    GREATEST(coverage_of_drawn, coverage_of_stored) DESC,
    id_recorrido
LIMIT $8::INTEGER;
