SELECT count(*)
FROM realtime_positions_batch p
WHERE NOT EXISTS (
    SELECT 1
    FROM vialis.equivalencias_recorridos_gcba e
    JOIN vialis.recorridos r
        ON r.gtfs_route_id = e.gtfs_route_id AND r.direction_id = e.gtfs_direction_id
    WHERE e.gcba_route_id = p.route_id
      AND e.gcba_direction = p.direction
);
