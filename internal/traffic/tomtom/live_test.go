package tomtom

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
)

func TestLiveClient(t *testing.T) {
	if os.Getenv("TOMTOM_LIVE_TEST") != "1" {
		t.Skip("set TOMTOM_LIVE_TEST=1 to call TomTom")
	}
	apiKey := os.Getenv("TOMTOM_API_KEY")
	if apiKey == "" {
		t.Fatal("TOMTOM_API_KEY is required for the live test")
	}
	client, err := NewClient(Options{
		APIKey:            apiKey,
		RequestTimeout:    config.TomTomRequestTimeout,
		CacheTTL:          config.TomTomTrafficTTL,
		CacheEntries:      config.TomTomCacheEntries,
		CacheBytes:        config.TomTomCacheBytes,
		RequestsPerSecond: config.TomTomRequestsPerSecond,
		MaximumTiles:      config.DetourMaximumTrafficTiles,
		Margin:            config.TomTomTileMargin,
		Limits: Limits{
			MaximumTileBytes: config.TomTomMaximumTileBytes,
			MaximumFeatures:  config.TomTomMaximumTileFeatures,
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	tile := Tile{Zoom: 14, X: 5531, Y: 9871}
	snapshot, err := client.Snapshot(context.Background(), []Tile{tile})
	if err != nil {
		t.Fatalf("live Snapshot returned error: %v", err)
	}
	if len(snapshot.Segments) == 0 {
		t.Fatal("live Snapshot returned no Traffic Flow segments")
	}
	t.Logf(
		"tile %s: %d segments, fetchedAt=%s, trafficAge=%s",
		tile,
		len(snapshot.Segments),
		snapshot.FetchedAt,
		snapshot.TrafficAge,
	)

	cached, err := client.Snapshot(context.Background(), []Tile{tile})
	if err != nil {
		t.Fatalf("cached Snapshot returned error: %v", err)
	}
	if cached.CacheHits != 1 {
		t.Fatalf("cached Snapshot cache hits = %d, want 1", cached.CacheHits)
	}
}
