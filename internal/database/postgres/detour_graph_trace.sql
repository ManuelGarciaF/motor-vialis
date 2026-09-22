SELECT
    count(*)::integer AS candidate_edges,
    count(*) FILTER (WHERE blocked)::integer AS blocked_edges,
    count(*) FILTER (
        WHERE forward_source = 'tomtom_direct' AND cost > 0
    )::integer AS forward_direct_edges,
    count(*) FILTER (
        WHERE reverse_source = 'tomtom_direct' AND reverse_cost > 0
    )::integer AS reverse_direct_edges,
    count(*) FILTER (
        WHERE forward_source = 'tomtom_nearby_estimate' AND cost > 0
    )::integer AS forward_estimated_edges,
    count(*) FILTER (
        WHERE reverse_source = 'tomtom_nearby_estimate' AND reverse_cost > 0
    )::integer AS reverse_estimated_edges
FROM detour_graph;
