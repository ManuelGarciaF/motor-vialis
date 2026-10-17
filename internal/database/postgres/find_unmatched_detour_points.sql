SELECT requested.id
FROM detour_requested_points requested
LEFT JOIN detour_points snapped ON snapped.pid = requested.id
WHERE snapped.pid IS NULL
ORDER BY requested.route_order
