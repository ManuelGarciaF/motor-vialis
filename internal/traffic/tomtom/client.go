package tomtom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	defaultBaseURL = "https://api.tomtom.com"
	flowStyle      = "absolute"
)

// Options configures provider limits and process-local caching.
type Options struct {
	APIKey            string
	BaseURL           string
	HTTPClient        *http.Client
	RequestTimeout    time.Duration
	CacheTTL          time.Duration
	CacheEntries      int
	CacheBytes        int
	RequestsPerSecond int
	MaximumTiles      int
	Margin            float64
	Limits            Limits
	Logger            *slog.Logger
	Now               func() time.Time
}

// Client downloads, caches, and decodes Traffic Flow tiles.
type Client struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	cache        *tileCache
	gate         *rateGate
	maximumTiles int
	margin       float64
	limits       Limits
	logger       *slog.Logger
	now          func() time.Time
}

// NewClient validates the provider configuration and creates one cache shared
// by every snapshot obtained through the client.
func NewClient(options Options) (*Client, error) {
	if options.BaseURL == "" {
		options.BaseURL = defaultBaseURL
	}
	parsedURL, err := url.Parse(options.BaseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("tomtom base URL is invalid")
	}
	if options.RequestTimeout <= 0 || options.CacheTTL <= 0 ||
		options.CacheEntries <= 0 || options.CacheBytes <= 0 ||
		options.RequestsPerSecond <= 0 || options.MaximumTiles <= 0 ||
		options.Limits.MaximumTileBytes <= 0 || options.Limits.MaximumFeatures <= 0 {
		return nil, fmt.Errorf("tomtom limits and durations must be positive")
	}
	if options.Margin < 0 || options.Margin > 0.1 {
		return nil, fmt.Errorf("tomtom tile margin must be between 0 and 0.1")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: options.RequestTimeout}
	} else {
		clientCopy := *options.HTTPClient
		clientCopy.Timeout = options.RequestTimeout
		options.HTTPClient = &clientCopy
	}
	return &Client{
		apiKey:       options.APIKey,
		baseURL:      strings.TrimRight(options.BaseURL, "/"),
		httpClient:   options.HTTPClient,
		cache:        newTileCache(options.CacheEntries, options.CacheBytes, options.CacheTTL),
		gate:         newRateGate(options.RequestsPerSecond),
		maximumTiles: options.MaximumTiles,
		margin:       options.Margin,
		limits:       options.Limits,
		logger:       options.Logger,
		now:          options.Now,
	}, nil
}

// Snapshot obtains and decodes a consistent set of tiles. Results preserve
// deterministic tile and feature ordering even though downloads run in parallel.
func (client *Client) Snapshot(ctx context.Context, tiles []Tile) (Snapshot, error) {
	if client.apiKey == "" {
		return Snapshot{}, &Error{
			Code:    ErrorAPIKeyMissing,
			Message: "TOMTOM_API_KEY is empty",
		}
	}
	ordered, err := normalizeTiles(tiles)
	if err != nil {
		return Snapshot{}, err
	}
	if len(ordered) == 0 {
		return Snapshot{}, fmt.Errorf("traffic snapshot requires at least one tile")
	}
	if len(ordered) > client.maximumTiles {
		return Snapshot{}, &Error{
			Code: ErrorTileLimit,
			Message: fmt.Sprintf(
				"snapshot requires %d tiles, maximum is %d",
				len(ordered),
				client.maximumTiles,
			),
		}
	}

	type tileResult struct {
		segments  []Segment
		fetchedAt time.Time
		cacheHit  bool
	}
	results := make([]tileResult, len(ordered))
	var apiRequests atomic.Int64
	group, groupContext := errgroup.WithContext(ctx)
	for index, tile := range ordered {
		index, tile := index, tile
		group.Go(func() error {
			key := cacheKey{Style: flowStyle, Tile: tile}
			entry, cacheHit, loadErr := client.cache.load(
				groupContext,
				key,
				client.now(),
				func() (cacheEntry, error) {
					if waitErr := client.gate.wait(groupContext); waitErr != nil {
						return cacheEntry{}, waitErr
					}
					apiRequests.Add(1)
					return client.fetch(groupContext, key)
				},
			)
			if loadErr != nil {
				return loadErr
			}
			segments, decodeErr := Decode(entry.data, tile, client.limits)
			if decodeErr != nil {
				client.cache.invalidate(key)
				return decodeErr
			}
			results[index] = tileResult{
				segments:  segments,
				fetchedAt: entry.fetchedAt,
				cacheHit:  cacheHit,
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		var providerError *Error
		if errors.As(err, &providerError) {
			return Snapshot{}, providerError
		}
		return Snapshot{}, &Error{
			Code:    ErrorUnavailable,
			Message: "could not obtain traffic snapshot",
			Cause:   err,
		}
	}

	now := client.now()
	snapshot := Snapshot{
		FetchedAt: results[0].fetchedAt,
		Zoom:      ordered[0].Zoom,
		TileCount: len(ordered),
	}
	for _, result := range results {
		if result.fetchedAt.Before(snapshot.FetchedAt) {
			snapshot.FetchedAt = result.fetchedAt
		}
		if result.cacheHit {
			snapshot.CacheHits++
		}
		snapshot.Segments = append(snapshot.Segments, result.segments...)
	}
	snapshot.TrafficAge = now.Sub(snapshot.FetchedAt)
	if snapshot.TrafficAge < 0 {
		snapshot.TrafficAge = 0
	}
	if snapshot.TrafficAge > client.cache.ttl {
		return Snapshot{}, &Error{
			Code:    ErrorStale,
			Message: "traffic snapshot exceeds its maximum age",
		}
	}
	client.logger.InfoContext(ctx, "TomTom traffic snapshot",
		"tiles", snapshot.TileCount,
		"apiRequests", apiRequests.Load(),
		"cacheHits", snapshot.CacheHits,
		"trafficAge", snapshot.TrafficAge,
	)
	return snapshot, nil
}

func (client *Client) fetch(ctx context.Context, key cacheKey) (cacheEntry, error) {
	endpoint, err := url.Parse(fmt.Sprintf(
		"%s/traffic/map/4/tile/flow/%s/%d/%d/%d.pbf",
		client.baseURL,
		key.Style,
		key.Tile.Zoom,
		key.Tile.X,
		key.Tile.Y,
	))
	if err != nil {
		return cacheEntry{}, &Error{Code: ErrorUnavailable, Message: "cannot build TomTom URL", Cause: err}
	}
	query := endpoint.Query()
	query.Set("key", client.apiKey)
	query.Set("margin", strconv.FormatFloat(client.margin, 'f', -1, 64))
	query.Set("tags", "[road_type,traffic_level,traffic_road_coverage,left_hand_traffic,road_closure,road_category,road_subcategory]")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return cacheEntry{}, &Error{Code: ErrorUnavailable, Message: "cannot create TomTom request", Cause: err}
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		cause := redactError(err, client.apiKey)
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		return cacheEntry{}, &Error{
			Code:    ErrorUnavailable,
			Message: fmt.Sprintf("request tile %s failed", key.Tile),
			Cause:   cause,
		}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return cacheEntry{}, &Error{
			Code: ErrorUnavailable,
			Message: fmt.Sprintf(
				"request tile %s returned %s",
				key.Tile,
				response.Status,
			),
		}
	}
	data, err := io.ReadAll(io.LimitReader(
		response.Body,
		int64(client.limits.MaximumTileBytes)+1,
	))
	if err != nil {
		return cacheEntry{}, &Error{
			Code:    ErrorUnavailable,
			Message: fmt.Sprintf("cannot read tile %s", key.Tile),
			Cause:   err,
		}
	}
	if len(data) > client.limits.MaximumTileBytes {
		return cacheEntry{}, invalidTile(key.Tile, fmt.Sprintf(
			"tile exceeds %d bytes",
			client.limits.MaximumTileBytes,
		), nil)
	}
	fetchedAt := client.now()
	return cacheEntry{
		key:       key,
		data:      data,
		fetchedAt: fetchedAt,
		etag:      response.Header.Get("ETag"),
		expires:   parseHTTPTime(response.Header.Get("Expires")),
	}, nil
}

func normalizeTiles(tiles []Tile) ([]Tile, error) {
	seen := make(map[Tile]struct{}, len(tiles))
	var zoom *int
	for _, tile := range tiles {
		if err := validateTile(tile); err != nil {
			return nil, err
		}
		if zoom != nil && tile.Zoom != *zoom {
			return nil, fmt.Errorf("traffic snapshot tiles must share one zoom")
		}
		value := tile.Zoom
		zoom = &value
		seen[tile] = struct{}{}
	}
	result := make([]Tile, 0, len(seen))
	for tile := range seen {
		result = append(result, tile)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Y != result[right].Y {
			return result[left].Y < result[right].Y
		}
		return result[left].X < result[right].X
	})
	return result, nil
}

func validateTile(tile Tile) error {
	if tile.Zoom < 0 || tile.Zoom > 22 {
		return fmt.Errorf("tile %s has invalid zoom", tile)
	}
	size := 1 << tile.Zoom
	if tile.X < 0 || tile.X >= size || tile.Y < 0 || tile.Y >= size {
		return fmt.Errorf("tile %s has invalid coordinates", tile)
	}
	return nil
}

func parseHTTPTime(value string) time.Time {
	parsed, err := http.ParseTime(value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func redactError(err error, secret string) error {
	if secret == "" {
		return err
	}
	message := strings.ReplaceAll(err.Error(), secret, "[REDACTED]")
	message = strings.ReplaceAll(message, url.QueryEscape(secret), "[REDACTED]")
	return errors.New(message)
}

type rateGate struct {
	mutex    sync.Mutex
	interval time.Duration
	next     time.Time
}

func newRateGate(requestsPerSecond int) *rateGate {
	return &rateGate{interval: time.Second / time.Duration(requestsPerSecond)}
}

func (gate *rateGate) wait(ctx context.Context) error {
	gate.mutex.Lock()
	now := time.Now()
	start := now
	if gate.next.After(start) {
		start = gate.next
	}
	gate.next = start.Add(gate.interval)
	gate.mutex.Unlock()

	delay := time.Until(start)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
