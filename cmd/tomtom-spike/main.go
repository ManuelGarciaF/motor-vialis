// Command tomtom-spike downloads and inspects TomTom Traffic Flow vector tiles.
// It is a temporary, reproducible probe for choosing the RF05 zoom and matching
// policy; it is not part of the HTTP service.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/maptile"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
)

const (
	defaultLatitude  = -34.6037
	defaultLongitude = -58.3816
	defaultRadius    = 1000.0
	defaultZooms     = "14,15,16"
	defaultMargin    = 0.1
	defaultMaxTiles  = 64
	maximumTileBytes = 20 << 20
	tomTomBaseURL    = "https://api.tomtom.com"
)

type options struct {
	latitude      float64
	longitude     float64
	radius        float64
	zooms         []int
	margin        float64
	maxTiles      int
	outputDir     string
	areaFile      string
	areaID        string
	viewerDir     string
	matchDatabase bool
	timeout       time.Duration
}

type tile struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

type report struct {
	GeneratedAt time.Time        `json:"generatedAt"`
	Center      *center          `json:"center,omitempty"`
	Radius      float64          `json:"radiusMeters,omitempty"`
	Area        *areaDescription `json:"area,omitempty"`
	Margin      float64          `json:"tileMargin"`
	Results     []zoomReport     `json:"results"`
}

type areaDescription struct {
	File      string `json:"file"`
	FeatureID string `json:"featureId,omitempty"`
}

type center struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type zoomReport struct {
	Zoom                 int            `json:"zoom"`
	ApproxTileWidthMeter float64        `json:"approxTileWidthMeters"`
	TileCount            int            `json:"tileCount"`
	ResponseBytes        int64          `json:"responseBytes"`
	TrafficFeatures      int            `json:"trafficFeatures"`
	LineParts            int            `json:"lineParts"`
	Closures             int            `json:"closures"`
	FeaturesWithoutSpeed int            `json:"featuresWithoutSpeed"`
	MinimumSpeedKPH      *float64       `json:"minimumSpeedKph,omitempty"`
	MaximumSpeedKPH      *float64       `json:"maximumSpeedKph,omitempty"`
	AverageSpeedKPH      *float64       `json:"averageSpeedKph,omitempty"`
	RoadTypes            map[string]int `json:"roadTypes"`
	RoadCategories       map[string]int `json:"roadCategories"`
	RoadSubcategories    map[string]int `json:"roadSubcategories"`
	RoadCoverage         map[string]int `json:"roadCoverage"`
	CacheHits            int            `json:"cacheHits"`
	GraphMatch           *matchReport   `json:"graphMatch,omitempty"`
	Duration             time.Duration  `json:"-"`
	DurationMillis       int64          `json:"durationMillis"`
	Tiles                []tile         `json:"tiles"`
}

type tileStats struct {
	bytes                int64
	features             int
	lineParts            int
	closures             int
	featuresWithoutSpeed int
	minimumSpeed         float64
	maximumSpeed         float64
	totalSpeed           float64
	speedCount           int
	roadTypes            map[string]int
	roadCategories       map[string]int
	roadSubcategories    map[string]int
	roadCoverage         map[string]int
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "tomtom-spike: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}

	cfg := config.FromEnv()
	apiKey := cfg.TomTomAPIKey
	if apiKey == "" {
		return errors.New("TOMTOM_API_KEY is empty; export .env or run through direnv")
	}

	var matcher *trafficMatcher
	if options.matchDatabase {
		matcher, err = newTrafficMatcher(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer matcher.Close(ctx)
	}

	var areaGeometry []byte
	if options.areaFile != "" {
		if matcher == nil {
			return errors.New("-area-file requires -match-db=true")
		}
		areaGeometry, err = loadAreaGeometry(options.areaFile, options.areaID)
		if err != nil {
			return err
		}
	}

	client := &http.Client{Timeout: options.timeout}
	result := report{
		GeneratedAt: time.Now().UTC(),
		Margin:      options.margin,
		Results:     make([]zoomReport, 0, len(options.zooms)),
	}
	if len(areaGeometry) != 0 {
		result.Area = &areaDescription{File: options.areaFile, FeatureID: options.areaID}
	} else {
		result.Center = &center{Latitude: options.latitude, Longitude: options.longitude}
		result.Radius = options.radius
	}

	for _, zoom := range options.zooms {
		var tiles []tile
		if len(areaGeometry) != 0 {
			tiles, err = matcher.TilesForArea(ctx, areaGeometry, zoom)
		} else {
			tiles, err = tilesAround(options.latitude, options.longitude, options.radius, zoom)
		}
		if err != nil {
			return fmt.Errorf("zoom %d: %w", zoom, err)
		}
		if len(tiles) > options.maxTiles {
			return fmt.Errorf(
				"zoom %d needs %d tiles, above -max-tiles=%d",
				zoom,
				len(tiles),
				options.maxTiles,
			)
		}

		startedAt := time.Now()
		zoomResult := newZoomReport(zoom, options.latitude, tiles)
		var trafficSegments []trafficSegment
		for _, tile := range tiles {
			data, cacheHit, err := loadOrFetchTile(
				ctx,
				client,
				apiKey,
				options.margin,
				tile,
				options.outputDir,
			)
			if err != nil {
				return err
			}
			if cacheHit {
				zoomResult.CacheHits++
			}

			stats, segments, err := inspectTile(data, tile)
			if err != nil {
				return fmt.Errorf("decode tile %d/%d/%d: %w", tile.Z, tile.X, tile.Y, err)
			}
			zoomResult.add(stats)
			trafficSegments = append(trafficSegments, segments...)
		}
		if matcher != nil {
			var match matchReport
			if len(areaGeometry) != 0 && options.viewerDir != "" {
				match, err = matcher.MatchAreaWithViewer(
					ctx,
					areaGeometry,
					trafficSegments,
					options.viewerDir,
				)
			} else if len(areaGeometry) != 0 {
				match, err = matcher.MatchArea(ctx, areaGeometry, trafficSegments)
			} else {
				match, err = matcher.Match(
					ctx,
					options.latitude,
					options.longitude,
					options.radius,
					trafficSegments,
				)
			}
			if err != nil {
				return fmt.Errorf("match zoom %d against vialis.calles: %w", zoom, err)
			}
			zoomResult.GraphMatch = &match
		}
		zoomResult.Duration = time.Since(startedAt)
		zoomResult.DurationMillis = zoomResult.Duration.Milliseconds()
		zoomResult.finish()
		result.Results = append(result.Results, zoomResult)
	}

	if options.viewerDir != "" {
		if err := writeJSONFile(filepath.Join(options.viewerDir, "report.json"), result); err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("tomtom-spike", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	latitude := flags.Float64("lat", defaultLatitude, "center latitude")
	longitude := flags.Float64("lon", defaultLongitude, "center longitude")
	radius := flags.Float64("radius", defaultRadius, "probe radius in meters")
	zoomValues := flags.String("zooms", defaultZooms, "comma-separated zoom levels")
	margin := flags.Float64("margin", defaultMargin, "TomTom tile margin from 0 to 0.1")
	maxTiles := flags.Int("max-tiles", defaultMaxTiles, "maximum tiles allowed per zoom")
	outputDir := flags.String("output-dir", "", "optional read-through cache for raw PBF tiles")
	areaFile := flags.String("area-file", "", "optional Polygon/Feature/FeatureCollection GeoJSON")
	areaID := flags.String("area-id", "", "feature properties.id selected from -area-file")
	viewerDir := flags.String("viewer-dir", "", "optional directory for local map viewer files")
	matchDatabase := flags.Bool("match-db", true, "compare traffic geometries with vialis.calles")
	timeout := flags.Duration("timeout", 15*time.Second, "timeout for each tile request")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *latitude < -85.05112878 || *latitude > 85.05112878 {
		return options{}, fmt.Errorf("-lat must be between -85.05112878 and 85.05112878")
	}
	if *longitude < -180 || *longitude > 180 {
		return options{}, fmt.Errorf("-lon must be between -180 and 180")
	}
	if *radius <= 0 {
		return options{}, fmt.Errorf("-radius must be positive")
	}
	if *margin < 0 || *margin > 0.1 {
		return options{}, fmt.Errorf("-margin must be between 0 and 0.1")
	}
	if *maxTiles <= 0 {
		return options{}, fmt.Errorf("-max-tiles must be positive")
	}
	if *timeout <= 0 {
		return options{}, fmt.Errorf("-timeout must be positive")
	}
	if *viewerDir != "" && *areaFile == "" {
		return options{}, fmt.Errorf("-viewer-dir requires -area-file")
	}
	zooms, err := parseZooms(*zoomValues)
	if err != nil {
		return options{}, err
	}
	return options{
		latitude:      *latitude,
		longitude:     *longitude,
		radius:        *radius,
		zooms:         zooms,
		margin:        *margin,
		maxTiles:      *maxTiles,
		outputDir:     *outputDir,
		areaFile:      *areaFile,
		areaID:        *areaID,
		viewerDir:     *viewerDir,
		matchDatabase: *matchDatabase,
		timeout:       *timeout,
	}, nil
}

func parseZooms(value string) ([]int, error) {
	seen := make(map[int]struct{})
	var zooms []int
	for _, raw := range strings.Split(value, ",") {
		zoom, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || zoom < 0 || zoom > 22 {
			return nil, fmt.Errorf("invalid zoom %q: expected integers from 0 to 22", raw)
		}
		if _, exists := seen[zoom]; exists {
			continue
		}
		seen[zoom] = struct{}{}
		zooms = append(zooms, zoom)
	}
	if len(zooms) == 0 {
		return nil, errors.New("-zooms must not be empty")
	}
	sort.Ints(zooms)
	return zooms, nil
}

func tilesAround(latitude, longitude, radiusMeters float64, zoom int) ([]tile, error) {
	const earthRadiusMeters = 6378137.0
	latitudeRadians := latitude * math.Pi / 180
	latitudeDelta := radiusMeters / earthRadiusMeters * 180 / math.Pi
	cosine := math.Cos(latitudeRadians)
	if math.Abs(cosine) < 1e-9 {
		return nil, errors.New("cannot calculate longitude extent near a pole")
	}
	longitudeDelta := radiusMeters / (earthRadiusMeters * cosine) * 180 / math.Pi

	minimumLatitude := math.Max(-85.05112878, latitude-latitudeDelta)
	maximumLatitude := math.Min(85.05112878, latitude+latitudeDelta)
	minimumLongitude := math.Max(-180, longitude-longitudeDelta)
	maximumLongitude := math.Min(180, longitude+longitudeDelta)
	if minimumLongitude > maximumLongitude {
		return nil, errors.New("probe bounds cross the antimeridian")
	}

	minimumX := longitudeToTileX(minimumLongitude, zoom)
	maximumX := longitudeToTileX(maximumLongitude, zoom)
	minimumY := latitudeToTileY(maximumLatitude, zoom)
	maximumY := latitudeToTileY(minimumLatitude, zoom)

	tiles := make([]tile, 0, (maximumX-minimumX+1)*(maximumY-minimumY+1))
	for y := minimumY; y <= maximumY; y++ {
		for x := minimumX; x <= maximumX; x++ {
			tiles = append(tiles, tile{X: x, Y: y, Z: zoom})
		}
	}
	return tiles, nil
}

func longitudeToTileX(longitude float64, zoom int) int {
	size := math.Exp2(float64(zoom))
	x := int(math.Floor((longitude + 180) / 360 * size))
	return max(0, min(int(size)-1, x))
}

func latitudeToTileY(latitude float64, zoom int) int {
	latitudeRadians := latitude * math.Pi / 180
	size := math.Exp2(float64(zoom))
	y := int(math.Floor((1 - math.Asinh(math.Tan(latitudeRadians))/math.Pi) / 2 * size))
	return max(0, min(int(size)-1, y))
}

func fetchTile(
	ctx context.Context,
	client *http.Client,
	apiKey string,
	margin float64,
	tile tile,
) ([]byte, error) {
	endpoint, err := url.Parse(fmt.Sprintf(
		"%s/traffic/map/4/tile/flow/absolute/%d/%d/%d.pbf",
		tomTomBaseURL,
		tile.Z,
		tile.X,
		tile.Y,
	))
	if err != nil {
		return nil, fmt.Errorf("build tile URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("key", apiKey)
	query.Set("margin", strconv.FormatFloat(margin, 'f', -1, 64))
	query.Set("tags", "[road_type,traffic_level,traffic_road_coverage,left_hand_traffic,road_closure,road_category,road_subcategory]")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request for tile %d/%d/%d: %w", tile.Z, tile.X, tile.Y, err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf(
			"fetch tile %d/%d/%d: %s",
			tile.Z,
			tile.X,
			tile.Y,
			redactSecret(err.Error(), apiKey),
		)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf(
			"fetch tile %d/%d/%d: TomTom returned %s: %s",
			tile.Z,
			tile.X,
			tile.Y,
			response.Status,
			redactSecret(strings.TrimSpace(string(message)), apiKey),
		)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maximumTileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read tile %d/%d/%d: %w", tile.Z, tile.X, tile.Y, err)
	}
	if len(data) > maximumTileBytes {
		return nil, fmt.Errorf("tile %d/%d/%d exceeds %d bytes", tile.Z, tile.X, tile.Y, maximumTileBytes)
	}
	return data, nil
}

func inspectTile(data []byte, tile tile) (tileStats, []trafficSegment, error) {
	layers, err := mvt.Unmarshal(data)
	if err != nil {
		return tileStats{}, nil, err
	}
	layers.ProjectToWGS84(maptile.New(uint32(tile.X), uint32(tile.Y), maptile.Zoom(tile.Z)))

	stats := tileStats{
		bytes:             int64(len(data)),
		minimumSpeed:      math.Inf(1),
		maximumSpeed:      math.Inf(-1),
		roadTypes:         make(map[string]int),
		roadCategories:    make(map[string]int),
		roadSubcategories: make(map[string]int),
		roadCoverage:      make(map[string]int),
	}
	var segments []trafficSegment
	for _, layer := range layers {
		if layer.Name != "Traffic flow" {
			continue
		}
		for featureIndex, feature := range layer.Features {
			stats.features++
			roadType := feature.Properties.MustString("road_type", "unknown")
			roadCategory := feature.Properties.MustString("road_category", "unknown")
			roadSubcategory := feature.Properties.MustString("road_subcategory", "unknown")
			stats.roadTypes[roadType]++
			stats.roadCategories[roadCategory]++
			stats.roadSubcategories[roadSubcategory]++
			coverage := feature.Properties.MustString("traffic_road_coverage", "unknown")
			stats.roadCoverage[coverage]++
			closure := feature.Properties.MustBool("road_closure", false)
			if closure {
				stats.closures++
			}
			speedValue, exists := feature.Properties["traffic_level"]
			speed, valid := numericValue(speedValue)
			if !exists || !valid {
				stats.featuresWithoutSpeed++
			} else {
				stats.minimumSpeed = math.Min(stats.minimumSpeed, speed)
				stats.maximumSpeed = math.Max(stats.maximumSpeed, speed)
				stats.totalSpeed += speed
				stats.speedCount++
			}

			appendLine := func(line orb.LineString) {
				if len(line) < 2 {
					return
				}
				stats.lineParts++
				segments = append(segments, trafficSegment{
					SourceID:        fmt.Sprintf("%d/%d/%d/%d", tile.Z, tile.X, tile.Y, featureIndex),
					Geometry:        line,
					RoadType:        roadType,
					RoadCategory:    roadCategory,
					RoadSubcategory: roadSubcategory,
					RoadCoverage:    coverage,
					SpeedKPH:        speed,
					HasSpeed:        exists && valid,
					Closure:         closure,
				})
			}
			switch geometry := feature.Geometry.(type) {
			case orb.LineString:
				appendLine(geometry)
			case orb.MultiLineString:
				for _, line := range geometry {
					appendLine(line)
				}
			}
		}
	}
	return stats, segments, nil
}

func redactSecret(message, secret string) string {
	if secret == "" {
		return message
	}
	message = strings.ReplaceAll(message, secret, "[REDACTED]")
	return strings.ReplaceAll(message, url.QueryEscape(secret), "[REDACTED]")
}

func numericValue(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint64:
		return float64(value), true
	default:
		return 0, false
	}
}

func newZoomReport(zoom int, latitude float64, tiles []tile) zoomReport {
	const earthCircumferenceMeters = 40075016.686
	return zoomReport{
		Zoom:                 zoom,
		ApproxTileWidthMeter: earthCircumferenceMeters * math.Cos(latitude*math.Pi/180) / math.Exp2(float64(zoom)),
		TileCount:            len(tiles),
		RoadTypes:            make(map[string]int),
		RoadCategories:       make(map[string]int),
		RoadSubcategories:    make(map[string]int),
		RoadCoverage:         make(map[string]int),
		Tiles:                tiles,
	}
}

func (report *zoomReport) add(stats tileStats) {
	report.ResponseBytes += stats.bytes
	report.TrafficFeatures += stats.features
	report.LineParts += stats.lineParts
	report.Closures += stats.closures
	report.FeaturesWithoutSpeed += stats.featuresWithoutSpeed
	for value, count := range stats.roadTypes {
		report.RoadTypes[value] += count
	}
	for value, count := range stats.roadCategories {
		report.RoadCategories[value] += count
	}
	for value, count := range stats.roadSubcategories {
		report.RoadSubcategories[value] += count
	}
	for value, count := range stats.roadCoverage {
		report.RoadCoverage[value] += count
	}
	if stats.speedCount == 0 {
		return
	}
	if report.MinimumSpeedKPH == nil || stats.minimumSpeed < *report.MinimumSpeedKPH {
		value := stats.minimumSpeed
		report.MinimumSpeedKPH = &value
	}
	if report.MaximumSpeedKPH == nil || stats.maximumSpeed > *report.MaximumSpeedKPH {
		value := stats.maximumSpeed
		report.MaximumSpeedKPH = &value
	}
	if report.AverageSpeedKPH == nil {
		value := 0.0
		report.AverageSpeedKPH = &value
	}
	// Accumulate speeds here; finish converts the sum to an average.
	*report.AverageSpeedKPH += stats.totalSpeed
}

func (report *zoomReport) finish() {
	if report.AverageSpeedKPH == nil {
		return
	}
	speedCount := report.TrafficFeatures - report.FeaturesWithoutSpeed
	if speedCount > 0 {
		*report.AverageSpeedKPH /= float64(speedCount)
	}
}

func loadOrFetchTile(
	ctx context.Context,
	client *http.Client,
	apiKey string,
	margin float64,
	tile tile,
	directory string,
) ([]byte, bool, error) {
	if directory != "" {
		path := tilePath(directory, margin, tile)
		data, err := os.ReadFile(path)
		if err == nil {
			if len(data) > maximumTileBytes {
				return nil, false, fmt.Errorf("cached tile %d/%d/%d exceeds %d bytes", tile.Z, tile.X, tile.Y, maximumTileBytes)
			}
			return data, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, false, fmt.Errorf("read cached tile %d/%d/%d: %w", tile.Z, tile.X, tile.Y, err)
		}
	}

	data, err := fetchTile(ctx, client, apiKey, margin, tile)
	if err != nil {
		return nil, false, err
	}
	if directory != "" {
		if err := saveTile(directory, margin, tile, data); err != nil {
			return nil, false, err
		}
	}
	return data, false, nil
}

func saveTile(directory string, margin float64, tile tile, data []byte) error {
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(tilePath(directory, margin, tile), data, 0o600); err != nil {
		return fmt.Errorf("write tile %d/%d/%d: %w", tile.Z, tile.X, tile.Y, err)
	}
	return nil
}

func tilePath(directory string, margin float64, tile tile) string {
	marginText := strings.ReplaceAll(strconv.FormatFloat(margin, 'f', -1, 64), ".", "_")
	return filepath.Join(
		directory,
		fmt.Sprintf("flow-absolute-margin-%s-%d-%d-%d.pbf", marginText, tile.Z, tile.X, tile.Y),
	)
}
