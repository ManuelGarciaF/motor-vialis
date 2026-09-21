CREATE TEMP TABLE detour_combinations ON COMMIT DROP AS
SELECT
    -(pair ->> 'from')::bigint AS source,
    -(pair ->> 'to')::bigint AS target
FROM jsonb_array_elements($1::jsonb) AS pair
JOIN detour_points origin ON origin.pid = (pair ->> 'from')::bigint
JOIN detour_points destination ON destination.pid = (pair ->> 'to')::bigint
WHERE (pair ->> 'from')::bigint <> (pair ->> 'to')::bigint;
