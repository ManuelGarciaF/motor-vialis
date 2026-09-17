package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

const (
	trafficMatchRadiusMeters      = 15.0
	trafficMatchMaximumDegrees    = 45.0
	trafficEstimateRadiusMeters   = 300.0
	trafficEstimateMaximumSamples = 5
	trafficEstimateMinimumSamples = 3
)

type trafficSegment struct {
	SourceID        string
	Geometry        orb.LineString
	RoadType        string
	RoadCategory    string
	RoadSubcategory string
	RoadCoverage    string
	SpeedKPH        float64
	HasSpeed        bool
	Closure         bool
}

type trafficMatcher struct {
	connection *pgx.Conn
}

type matchReport struct {
	GraphEdges                int64                    `json:"graphEdges"`
	GraphLengthMeters         float64                  `json:"graphLengthMeters"`
	EdgesWithNearbyTraffic    int64                    `json:"edgesWithNearbyTraffic"`
	NearbyEdgePercent         float64                  `json:"nearbyEdgePercent"`
	MatchedEdges              int64                    `json:"matchedEdges"`
	MatchedEdgePercent        float64                  `json:"matchedEdgePercent"`
	MatchedLengthPercent      float64                  `json:"matchedLengthPercent"`
	MedianDistanceMeters      *float64                 `json:"medianDistanceMeters,omitempty"`
	Percentile95DistanceMeter *float64                 `json:"percentile95DistanceMeters,omitempty"`
	OriginalComponents        int64                    `json:"originalComponents"`
	OriginalVertices          int64                    `json:"originalVertices"`
	OriginalLargestComponent  float64                  `json:"originalLargestComponentVertexPercent"`
	MatchedComponents         int64                    `json:"matchedComponents"`
	MatchedVertices           int64                    `json:"matchedVertices"`
	MatchedVertexCoverage     float64                  `json:"matchedVertexCoveragePercent"`
	MatchedLargestComponent   float64                  `json:"matchedLargestComponentVertexPercent"`
	MatchedLargestOfOriginal  float64                  `json:"matchedLargestOfOriginalVertexPercent"`
	SelectedTrafficRoadTypes  map[string]int64         `json:"selectedTrafficRoadTypes"`
	SelectedRoadCoverage      map[string]int64         `json:"selectedRoadCoverage"`
	GraphRoadTypes            map[string]roadTypeMatch `json:"graphRoadTypes"`
	NearbyEstimation          estimationReport         `json:"nearbyEstimation"`
}

type estimationReport struct {
	RadiusMeters                   float64  `json:"radiusMeters"`
	MinimumSamples                 int      `json:"minimumSamples"`
	MaximumSamples                 int      `json:"maximumSamples"`
	UnmatchedEdges                 int64    `json:"unmatchedEdges"`
	EstimatedEdges                 int64    `json:"estimatedEdges"`
	EstimatedUnmatchedEdgePercent  float64  `json:"estimatedUnmatchedEdgePercent"`
	DirectOrEstimatedEdgePercent   float64  `json:"directOrEstimatedEdgePercent"`
	DirectOrEstimatedLengthPercent float64  `json:"directOrEstimatedLengthPercent"`
	ValidationEdges                int64    `json:"validationEdges"`
	ValidationEstimatedEdges       int64    `json:"validationEstimatedEdges"`
	ValidationCoveragePercent      float64  `json:"validationCoveragePercent"`
	MeanAbsoluteErrorKPH           *float64 `json:"meanAbsoluteErrorKph,omitempty"`
	MedianAbsoluteErrorKPH         *float64 `json:"medianAbsoluteErrorKph,omitempty"`
	Percentile90AbsoluteErrorKPH   *float64 `json:"percentile90AbsoluteErrorKph,omitempty"`
	MeanBiasKPH                    *float64 `json:"meanBiasKph,omitempty"`
	Within5KPHPercent              float64  `json:"within5KphPercent"`
	Within10KPHPercent             float64  `json:"within10KphPercent"`
	CoveredVertices                int64    `json:"coveredVertices"`
	CoveredVertexPercent           float64  `json:"coveredVertexPercent"`
	Components                     int64    `json:"components"`
	LargestOfOriginalVertexPercent float64  `json:"largestOfOriginalVertexPercent"`
}

type roadTypeMatch struct {
	Edges                int64   `json:"edges"`
	MatchedEdges         int64   `json:"matchedEdges"`
	MatchedEdgePercent   float64 `json:"matchedEdgePercent"`
	MatchedLengthPercent float64 `json:"matchedLengthPercent"`
}

func newTrafficMatcher(ctx context.Context, databaseURL string) (*trafficMatcher, error) {
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL for matching: %w", err)
	}
	return &trafficMatcher{connection: connection}, nil
}

func (matcher *trafficMatcher) Close(ctx context.Context) {
	_ = matcher.connection.Close(ctx)
}

func (matcher *trafficMatcher) TilesForArea(
	ctx context.Context,
	areaGeometry []byte,
	zoom int,
) ([]tile, error) {
	rows, err := matcher.connection.Query(ctx, tilesForAreaSQL, areaGeometry, zoom)
	if err != nil {
		return nil, fmt.Errorf("enumerate area tiles: %w", err)
	}
	defer rows.Close()

	var tiles []tile
	for rows.Next() {
		var value tile
		if err := rows.Scan(&value.X, &value.Y); err != nil {
			return nil, fmt.Errorf("scan area tile: %w", err)
		}
		value.Z = zoom
		tiles = append(tiles, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read area tiles: %w", err)
	}
	return tiles, nil
}

func (matcher *trafficMatcher) Match(
	ctx context.Context,
	latitude, longitude, radiusMeters float64,
	segments []trafficSegment,
) (matchReport, error) {
	return matcher.match(ctx, nil, latitude, longitude, radiusMeters, segments, "")
}

func (matcher *trafficMatcher) MatchArea(
	ctx context.Context,
	areaGeometry []byte,
	segments []trafficSegment,
) (matchReport, error) {
	return matcher.match(ctx, areaGeometry, 0, 0, 0, segments, "")
}

func (matcher *trafficMatcher) MatchAreaWithViewer(
	ctx context.Context,
	areaGeometry []byte,
	segments []trafficSegment,
	viewerDirectory string,
) (matchReport, error) {
	return matcher.match(ctx, areaGeometry, 0, 0, 0, segments, viewerDirectory)
}

func (matcher *trafficMatcher) match(
	ctx context.Context,
	areaGeometry []byte,
	latitude, longitude, radiusMeters float64,
	segments []trafficSegment,
	viewerDirectory string,
) (matchReport, error) {
	if len(segments) == 0 {
		return matchReport{}, fmt.Errorf("traffic snapshot has no LineString parts")
	}
	featureCollection := geojson.NewFeatureCollection()
	for index, segment := range segments {
		feature := geojson.NewFeature(segment.Geometry)
		feature.ID = index + 1
		feature.Properties = geojson.Properties{
			"source_id":             segment.SourceID,
			"road_type":             segment.RoadType,
			"road_category":         segment.RoadCategory,
			"road_subcategory":      segment.RoadSubcategory,
			"traffic_road_coverage": segment.RoadCoverage,
			"traffic_level":         segment.SpeedKPH,
			"has_speed":             segment.HasSpeed,
			"road_closure":          segment.Closure,
		}
		featureCollection.Append(feature)
	}
	encoded, err := json.Marshal(featureCollection)
	if err != nil {
		return matchReport{}, fmt.Errorf("encode traffic segments as GeoJSON: %w", err)
	}

	transaction, err := matcher.connection.Begin(ctx)
	if err != nil {
		return matchReport{}, fmt.Errorf("begin matching transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if _, err := transaction.Exec(ctx, createTrafficSegmentsSQL, encoded); err != nil {
		return matchReport{}, fmt.Errorf("load temporary traffic segments: %w", err)
	}
	if _, err := transaction.Exec(ctx, indexTrafficSegmentsSQL); err != nil {
		return matchReport{}, fmt.Errorf("index temporary traffic segments: %w", err)
	}
	if _, err := transaction.Exec(
		ctx,
		createMatchesSQL,
		nullableJSON(areaGeometry),
		longitude,
		latitude,
		radiusMeters,
		trafficMatchRadiusMeters,
		trafficMatchMaximumDegrees,
	); err != nil {
		return matchReport{}, fmt.Errorf("match traffic segments: %w", err)
	}
	if _, err := transaction.Exec(ctx, indexMatchesSQL); err != nil {
		return matchReport{}, fmt.Errorf("index traffic matches: %w", err)
	}
	if _, err := transaction.Exec(
		ctx,
		createEstimatesSQL,
		trafficEstimateRadiusMeters,
		trafficEstimateMaximumSamples,
		trafficEstimateMinimumSamples,
	); err != nil {
		return matchReport{}, fmt.Errorf("estimate unmatched traffic speeds: %w", err)
	}
	if viewerDirectory != "" {
		if err := exportViewerFiles(ctx, transaction, viewerDirectory, segments); err != nil {
			return matchReport{}, err
		}
	}

	result := matchReport{
		SelectedTrafficRoadTypes: make(map[string]int64),
		SelectedRoadCoverage:     make(map[string]int64),
		GraphRoadTypes:           make(map[string]roadTypeMatch),
		NearbyEstimation: estimationReport{
			RadiusMeters:   trafficEstimateRadiusMeters,
			MinimumSamples: trafficEstimateMinimumSamples,
			MaximumSamples: trafficEstimateMaximumSamples,
		},
	}
	if err := transaction.QueryRow(ctx, aggregateMatchSQL).Scan(
		&result.GraphEdges,
		&result.GraphLengthMeters,
		&result.EdgesWithNearbyTraffic,
		&result.NearbyEdgePercent,
		&result.MatchedEdges,
		&result.MatchedEdgePercent,
		&result.MatchedLengthPercent,
		&result.MedianDistanceMeters,
		&result.Percentile95DistanceMeter,
	); err != nil {
		return matchReport{}, fmt.Errorf("aggregate traffic matching: %w", err)
	}
	if err := transaction.QueryRow(ctx, estimationSummarySQL, trafficEstimateMinimumSamples).Scan(
		&result.NearbyEstimation.UnmatchedEdges,
		&result.NearbyEstimation.EstimatedEdges,
		&result.NearbyEstimation.EstimatedUnmatchedEdgePercent,
		&result.NearbyEstimation.DirectOrEstimatedEdgePercent,
		&result.NearbyEstimation.DirectOrEstimatedLengthPercent,
		&result.NearbyEstimation.ValidationEdges,
		&result.NearbyEstimation.ValidationEstimatedEdges,
		&result.NearbyEstimation.ValidationCoveragePercent,
		&result.NearbyEstimation.MeanAbsoluteErrorKPH,
		&result.NearbyEstimation.MedianAbsoluteErrorKPH,
		&result.NearbyEstimation.Percentile90AbsoluteErrorKPH,
		&result.NearbyEstimation.MeanBiasKPH,
		&result.NearbyEstimation.Within5KPHPercent,
		&result.NearbyEstimation.Within10KPHPercent,
	); err != nil {
		return matchReport{}, fmt.Errorf("summarize nearby speed estimation: %w", err)
	}
	if err := transaction.QueryRow(ctx, componentSummarySQL).Scan(
		&result.OriginalComponents,
		&result.OriginalVertices,
		&result.OriginalLargestComponent,
		&result.MatchedComponents,
		&result.MatchedVertices,
		&result.MatchedVertexCoverage,
		&result.MatchedLargestComponent,
		&result.MatchedLargestOfOriginal,
		&result.NearbyEstimation.Components,
		&result.NearbyEstimation.CoveredVertices,
		&result.NearbyEstimation.CoveredVertexPercent,
		&result.NearbyEstimation.LargestOfOriginalVertexPercent,
	); err != nil {
		return matchReport{}, fmt.Errorf("measure traffic graph connectivity: %w", err)
	}

	rows, err := transaction.Query(ctx, selectedTrafficTypesSQL)
	if err != nil {
		return matchReport{}, fmt.Errorf("group selected traffic types: %w", err)
	}
	for rows.Next() {
		var roadType, coverage string
		var count int64
		if err := rows.Scan(&roadType, &coverage, &count); err != nil {
			rows.Close()
			return matchReport{}, fmt.Errorf("scan selected traffic type: %w", err)
		}
		result.SelectedTrafficRoadTypes[roadType] += count
		result.SelectedRoadCoverage[coverage] += count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return matchReport{}, fmt.Errorf("read selected traffic types: %w", err)
	}
	rows.Close()

	rows, err = transaction.Query(ctx, graphRoadTypesSQL)
	if err != nil {
		return matchReport{}, fmt.Errorf("group graph road types: %w", err)
	}
	for rows.Next() {
		var roadType string
		var values roadTypeMatch
		if err := rows.Scan(
			&roadType,
			&values.Edges,
			&values.MatchedEdges,
			&values.MatchedEdgePercent,
			&values.MatchedLengthPercent,
		); err != nil {
			rows.Close()
			return matchReport{}, fmt.Errorf("scan graph road type: %w", err)
		}
		result.GraphRoadTypes[roadType] = values
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return matchReport{}, fmt.Errorf("read graph road types: %w", err)
	}
	rows.Close()

	if err := transaction.Commit(ctx); err != nil {
		return matchReport{}, fmt.Errorf("commit matching transaction: %w", err)
	}
	return result, nil
}

func nullableJSON(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	return data
}

const tilesForAreaSQL = `
WITH area AS (
    SELECT ST_SetSRID(ST_GeomFromGeoJSON($1::jsonb), 4326) AS geom
), bounds AS (
    SELECT
        $2::integer AS z,
        floor((ST_XMin(geom) + 180) / 360 * power(2, $2))::integer AS minimum_x,
        floor((ST_XMax(geom) + 180) / 360 * power(2, $2))::integer AS maximum_x,
        floor((1 - asinh(tan(radians(ST_YMax(geom)))) / pi()) / 2 * power(2, $2))::integer AS minimum_y,
        floor((1 - asinh(tan(radians(ST_YMin(geom)))) / pi()) / 2 * power(2, $2))::integer AS maximum_y,
        geom
    FROM area
)
SELECT x, y
FROM bounds b
CROSS JOIN LATERAL generate_series(b.minimum_x, b.maximum_x) AS x
CROSS JOIN LATERAL generate_series(b.minimum_y, b.maximum_y) AS y
WHERE ST_Intersects(
    ST_Transform(ST_TileEnvelope(b.z, x, y), 4326),
    b.geom
)
ORDER BY y, x;
`

const createTrafficSegmentsSQL = `
CREATE TEMP TABLE tomtom_spike_segments ON COMMIT DROP AS
SELECT
    ordinality::bigint AS id,
    ST_SetSRID(
        ST_GeomFromGeoJSON(feature -> 'geometry'),
        4326
    )::geometry(LineString, 4326) AS geom,
    COALESCE(feature -> 'properties' ->> 'source_id', ordinality::text) AS source_id,
    COALESCE(feature -> 'properties' ->> 'road_type', 'unknown') AS road_type,
    COALESCE(feature -> 'properties' ->> 'road_category', 'unknown') AS road_category,
    COALESCE(feature -> 'properties' ->> 'road_subcategory', 'unknown') AS road_subcategory,
    COALESCE(feature -> 'properties' ->> 'traffic_road_coverage', 'unknown') AS road_coverage,
    COALESCE((feature -> 'properties' ->> 'traffic_level')::double precision, 0) AS speed_kph,
    COALESCE((feature -> 'properties' ->> 'has_speed')::boolean, false) AS has_speed,
    COALESCE((feature -> 'properties' ->> 'road_closure')::boolean, false) AS road_closure
FROM jsonb_array_elements($1::jsonb -> 'features')
     WITH ORDINALITY AS item(feature, ordinality);
`

const indexTrafficSegmentsSQL = `
CREATE INDEX tomtom_spike_segments_geom_idx
ON tomtom_spike_segments USING GIST (geom);
`

const createMatchesSQL = `
CREATE TEMP TABLE tomtom_spike_matches ON COMMIT DROP AS
WITH probe AS (
    SELECT
        CASE
            WHEN $1::jsonb IS NULL THEN ST_Buffer(
                ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography,
                $4::double precision
            )::geometry
            ELSE ST_SetSRID(ST_GeomFromGeoJSON($1::jsonb), 4326)
        END AS area,
        $5::double precision AS match_meters,
        $6::double precision AS maximum_angle_degrees
), area_edges AS (
    SELECT
        c.id_calle,
        c.tipo,
        c.geom,
        ST_Length(c.geom::geography) AS length_meters
    FROM vialis.calles c
    CROSS JOIN probe p
    WHERE c.geom && p.area
      AND ST_Intersects(c.geom, p.area)
)
SELECT
    e.id_calle,
    e.tipo,
    e.length_meters,
    EXISTS (
        SELECT 1
        FROM tomtom_spike_segments nearby
        CROSS JOIN probe p
        WHERE nearby.geom && ST_Expand(e.geom, p.match_meters / 90000.0)
          AND ST_DWithin(nearby.geom::geography, e.geom::geography, p.match_meters)
    ) AS has_nearby_traffic,
    matched.id AS traffic_id,
    matched.distance_meters,
    matched.angle_degrees,
    matched.source_id AS traffic_source_id,
    matched.road_type AS traffic_road_type,
    matched.road_category AS traffic_road_category,
    matched.road_subcategory AS traffic_road_subcategory,
    matched.road_coverage AS traffic_road_coverage,
    matched.speed_kph AS traffic_speed_kph,
    matched.has_speed AS traffic_has_speed,
    matched.road_closure AS traffic_road_closure
FROM area_edges e
LEFT JOIN LATERAL (
    SELECT candidate.*
    FROM (
        SELECT
            t.id,
            t.source_id,
            t.road_type,
            t.road_category,
            t.road_subcategory,
            t.road_coverage,
            t.speed_kph,
            t.has_speed,
            t.road_closure,
            ST_Distance(t.geom::geography, e.geom::geography) AS distance_meters,
            degrees(acos(LEAST(
                1.0,
                abs(cos(
                    ST_Azimuth(ST_StartPoint(t.geom), ST_EndPoint(t.geom))
                    - ST_Azimuth(ST_StartPoint(e.geom), ST_EndPoint(e.geom))
                ))
            ))) AS angle_degrees
        FROM tomtom_spike_segments t
        CROSS JOIN probe p
        WHERE t.geom && ST_Expand(e.geom, p.match_meters / 90000.0)
          AND ST_DWithin(t.geom::geography, e.geom::geography, p.match_meters)
    ) candidate
    CROSS JOIN probe p
    WHERE candidate.angle_degrees <= p.maximum_angle_degrees
    ORDER BY candidate.distance_meters, candidate.id
    LIMIT 1
) matched ON true;
`

const indexMatchesSQL = `
CREATE INDEX tomtom_spike_matches_edge_idx
ON tomtom_spike_matches (id_calle);
`

const createEstimatesSQL = `
CREATE TEMP TABLE tomtom_spike_estimates ON COMMIT DROP AS
WITH targets AS (
    SELECT
        m.*,
        c.geom,
        CASE
            WHEN c.tipo IN ('motorway', 'motorway_link') THEN 'motorway'
            WHEN c.tipo IN ('trunk', 'trunk_link') THEN 'trunk'
            WHEN c.tipo IN ('primary', 'primary_link') THEN 'primary'
            WHEN c.tipo IN ('secondary', 'secondary_link') THEN 'secondary'
            WHEN c.tipo IN ('tertiary', 'tertiary_link') THEN 'tertiary'
            WHEN c.tipo IN ('residential', 'living_street', 'unclassified') THEN 'street'
            ELSE NULL
        END AS compatible_category
    FROM tomtom_spike_matches m
    JOIN vialis.calles c USING (id_calle)
)
SELECT
    target.id_calle,
    target.tipo,
    target.length_meters,
    target.traffic_id,
    target.traffic_speed_kph AS direct_speed_kph,
    target.traffic_has_speed AS direct_has_speed,
    target.traffic_road_closure AS direct_road_closure,
    estimate.sample_count,
    estimate.estimated_speed_kph,
    COALESCE(estimate.sample_count >= $3::integer, false) AS estimated_usable
FROM targets target
LEFT JOIN LATERAL (
    SELECT
        count(*)::integer AS sample_count,
        percentile_cont(0.5) WITHIN GROUP (ORDER BY nearest.speed_kph) AS estimated_speed_kph
    FROM (
        SELECT distinct_sources.source_id, distinct_sources.speed_kph
        FROM (
            SELECT DISTINCT ON (t.source_id)
                t.source_id,
                t.speed_kph,
                ST_Distance(t.geom::geography, target.geom::geography) AS distance_meters
            FROM tomtom_spike_segments t
            WHERE target.compatible_category IS NOT NULL
              AND t.road_category = target.compatible_category
              AND t.has_speed
              AND NOT t.road_closure
              AND t.speed_kph > 0
              AND t.source_id IS DISTINCT FROM target.traffic_source_id
              AND t.geom && ST_Expand(target.geom, $1::double precision / 90000.0)
              AND ST_DWithin(t.geom::geography, target.geom::geography, $1::double precision)
            ORDER BY t.source_id, distance_meters
        ) distinct_sources
        ORDER BY distinct_sources.distance_meters, distinct_sources.source_id
        LIMIT $2::integer
    ) nearest
) estimate ON true;
`

const estimationSummarySQL = `
SELECT
    count(*) FILTER (WHERE traffic_id IS NULL)::bigint AS unmatched_edges,
    count(*) FILTER (
        WHERE traffic_id IS NULL AND sample_count >= $1
    )::bigint AS estimated_edges,
    COALESCE(
        100.0 * count(*) FILTER (WHERE traffic_id IS NULL AND sample_count >= $1)
        / NULLIF(count(*) FILTER (WHERE traffic_id IS NULL), 0),
        0
    )::double precision AS estimated_unmatched_edge_percent,
    COALESCE(
        100.0 * count(*) FILTER (WHERE traffic_id IS NOT NULL OR sample_count >= $1)
        / NULLIF(count(*), 0),
        0
    )::double precision AS direct_or_estimated_edge_percent,
    COALESCE(
        100.0 * sum(length_meters) FILTER (WHERE traffic_id IS NOT NULL OR sample_count >= $1)
        / NULLIF(sum(length_meters), 0),
        0
    )::double precision AS direct_or_estimated_length_percent,
    count(*) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
    )::bigint AS validation_edges,
    count(*) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
    )::bigint AS validation_estimated_edges,
    COALESCE(
        100.0 * count(*) FILTER (
            WHERE traffic_id IS NOT NULL AND direct_has_speed
              AND NOT direct_road_closure AND direct_speed_kph > 0
              AND sample_count >= $1
        ) / NULLIF(count(*) FILTER (
            WHERE traffic_id IS NOT NULL AND direct_has_speed
              AND NOT direct_road_closure AND direct_speed_kph > 0
        ), 0),
        0
    )::double precision AS validation_coverage_percent,
    avg(abs(estimated_speed_kph - direct_speed_kph)) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
    )::double precision AS mean_absolute_error_kph,
    percentile_cont(0.5) WITHIN GROUP (
        ORDER BY abs(estimated_speed_kph - direct_speed_kph)
    ) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
    ) AS median_absolute_error_kph,
    percentile_cont(0.9) WITHIN GROUP (
        ORDER BY abs(estimated_speed_kph - direct_speed_kph)
    ) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
    ) AS percentile_90_absolute_error_kph,
    avg(estimated_speed_kph - direct_speed_kph) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
    )::double precision AS mean_bias_kph,
    COALESCE(100.0 * count(*) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
          AND abs(estimated_speed_kph - direct_speed_kph) <= 5
    ) / NULLIF(count(*) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
    ), 0), 0)::double precision AS within_5_kph_percent,
    COALESCE(100.0 * count(*) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
          AND abs(estimated_speed_kph - direct_speed_kph) <= 10
    ) / NULLIF(count(*) FILTER (
        WHERE traffic_id IS NOT NULL AND direct_has_speed
          AND NOT direct_road_closure AND direct_speed_kph > 0
          AND sample_count >= $1
    ), 0), 0)::double precision AS within_10_kph_percent
FROM tomtom_spike_estimates;
`

const aggregateMatchSQL = `
SELECT
    count(*)::bigint,
    COALESCE(sum(length_meters), 0)::double precision,
    count(*) FILTER (WHERE has_nearby_traffic)::bigint,
    COALESCE(100.0 * count(*) FILTER (WHERE has_nearby_traffic) / NULLIF(count(*), 0), 0)::double precision,
    count(*) FILTER (WHERE traffic_id IS NOT NULL)::bigint,
    COALESCE(100.0 * count(*) FILTER (WHERE traffic_id IS NOT NULL) / NULLIF(count(*), 0), 0)::double precision,
    COALESCE(
        100.0 * sum(length_meters) FILTER (WHERE traffic_id IS NOT NULL)
        / NULLIF(sum(length_meters), 0),
        0
    )::double precision,
    percentile_cont(0.5) WITHIN GROUP (ORDER BY distance_meters)
        FILTER (WHERE traffic_id IS NOT NULL),
    percentile_cont(0.95) WITHIN GROUP (ORDER BY distance_meters)
        FILTER (WHERE traffic_id IS NOT NULL)
FROM tomtom_spike_matches;
`

const componentSummarySQL = `
WITH original_components AS (
    SELECT component, count(*)::bigint AS vertices
    FROM pgr_connectedComponents(
        'SELECT m.id_calle AS id, c.origen AS source, c.destino AS target, 1::float8 AS cost, 1::float8 AS reverse_cost
         FROM tomtom_spike_matches m
         JOIN vialis.calles c USING (id_calle)'
    )
    GROUP BY component
), matched_components AS (
    SELECT component, count(*)::bigint AS vertices
    FROM pgr_connectedComponents(
        'SELECT m.id_calle AS id, c.origen AS source, c.destino AS target, 1::float8 AS cost, 1::float8 AS reverse_cost
         FROM tomtom_spike_matches m
         JOIN vialis.calles c USING (id_calle)
         WHERE m.traffic_id IS NOT NULL'
    )
    GROUP BY component
), estimated_components AS (
    SELECT component, count(*)::bigint AS vertices
    FROM pgr_connectedComponents(
        'SELECT e.id_calle AS id, c.origen AS source, c.destino AS target, 1::float8 AS cost, 1::float8 AS reverse_cost
         FROM tomtom_spike_estimates e
         JOIN vialis.calles c USING (id_calle)
         WHERE e.traffic_id IS NOT NULL OR e.estimated_usable'
    )
    GROUP BY component
), original_summary AS (
    SELECT
        count(*)::bigint AS components,
        COALESCE(sum(vertices), 0)::bigint AS vertices,
        COALESCE(100.0 * max(vertices) / NULLIF(sum(vertices), 0), 0)::double precision AS largest_percent
    FROM original_components
), matched_summary AS (
    SELECT
        count(*)::bigint AS components,
        COALESCE(sum(vertices), 0)::bigint AS vertices,
        COALESCE(max(vertices), 0)::bigint AS largest_vertices,
        COALESCE(100.0 * max(vertices) / NULLIF(sum(vertices), 0), 0)::double precision AS largest_percent
    FROM matched_components
), estimated_summary AS (
    SELECT
        count(*)::bigint AS components,
        COALESCE(sum(vertices), 0)::bigint AS vertices,
        COALESCE(max(vertices), 0)::bigint AS largest_vertices
    FROM estimated_components
)
SELECT
    original_summary.components,
    original_summary.vertices,
    original_summary.largest_percent,
    matched_summary.components,
    matched_summary.vertices,
    COALESCE(100.0 * matched_summary.vertices / NULLIF(original_summary.vertices, 0), 0)::double precision,
    matched_summary.largest_percent,
    COALESCE(100.0 * matched_summary.largest_vertices / NULLIF(original_summary.vertices, 0), 0)::double precision,
    estimated_summary.components,
    estimated_summary.vertices,
    COALESCE(100.0 * estimated_summary.vertices / NULLIF(original_summary.vertices, 0), 0)::double precision,
    COALESCE(100.0 * estimated_summary.largest_vertices / NULLIF(original_summary.vertices, 0), 0)::double precision
FROM original_summary
CROSS JOIN matched_summary
CROSS JOIN estimated_summary;
`

const selectedTrafficTypesSQL = `
SELECT traffic_road_type, traffic_road_coverage, count(*)::bigint
FROM tomtom_spike_matches
WHERE traffic_id IS NOT NULL
GROUP BY traffic_road_type, traffic_road_coverage
ORDER BY traffic_road_type, traffic_road_coverage;
`

const graphRoadTypesSQL = `
SELECT
    tipo,
    count(*)::bigint AS edges,
    count(*) FILTER (WHERE traffic_id IS NOT NULL)::bigint AS matched_edges,
    COALESCE(
        100.0 * count(*) FILTER (WHERE traffic_id IS NOT NULL) / NULLIF(count(*), 0),
        0
    )::double precision AS matched_edge_percent,
    COALESCE(
        100.0 * sum(length_meters) FILTER (WHERE traffic_id IS NOT NULL)
        / NULLIF(sum(length_meters), 0),
        0
    )::double precision AS matched_length_percent
FROM tomtom_spike_matches
GROUP BY tipo
ORDER BY tipo;
`
