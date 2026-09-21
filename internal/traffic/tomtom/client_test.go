package tomtom

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientSnapshotDownloadsThenReusesCache(t *testing.T) {
	fixture, err := os.ReadFile("testdata/traffic-flow-anonymized.pbf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Query().Get("key") != "test-key" {
			t.Errorf("request key was not set")
		}
		if !strings.Contains(request.URL.Path, "/flow/absolute/14/8192/8192.pbf") {
			t.Errorf("unexpected path %q", request.URL.Path)
		}
		writer.Header().Set("ETag", `"fixture"`)
		_, _ = writer.Write(fixture)
	}))
	defer server.Close()

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	client := newTestClient(t, server.URL, func() time.Time { return now })
	tile := Tile{Zoom: 14, X: 8192, Y: 8192}
	first, err := client.Snapshot(context.Background(), []Tile{tile, tile})
	if err != nil {
		t.Fatalf("first Snapshot returned error: %v", err)
	}
	if first.TileCount != 1 || first.CacheHits != 0 || len(first.Segments) != 4 {
		t.Fatalf("first snapshot = %#v", first)
	}

	now = now.Add(5 * time.Minute)
	second, err := client.Snapshot(context.Background(), []Tile{tile})
	if err != nil {
		t.Fatalf("second Snapshot returned error: %v", err)
	}
	if second.CacheHits != 1 || second.TrafficAge != 5*time.Minute {
		t.Fatalf("second snapshot = %#v", second)
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d, want 1", requests.Load())
	}
}

func TestClientSnapshotRefreshesExpiredTile(t *testing.T) {
	fixture, err := os.ReadFile("testdata/traffic-flow-anonymized.pbf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(fixture)
	}))
	defer server.Close()

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	client := newTestClient(t, server.URL, func() time.Time { return now })
	tile := Tile{Zoom: 14, X: 8192, Y: 8192}
	if _, err := client.Snapshot(context.Background(), []Tile{tile}); err != nil {
		t.Fatalf("first Snapshot returned error: %v", err)
	}
	now = now.Add(31 * time.Minute)
	if _, err := client.Snapshot(context.Background(), []Tile{tile}); err != nil {
		t.Fatalf("second Snapshot returned error: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("total HTTP requests = %d, want 2", requests.Load())
	}
}

func TestClientSnapshotReportsConfigurationAndProviderErrors(t *testing.T) {
	client := newTestClient(t, "http://example.test", time.Now)
	client.apiKey = ""
	_, err := client.Snapshot(context.Background(), []Tile{{Zoom: 14, X: 1, Y: 1}})
	assertProviderCode(t, err, ErrorAPIKeyMissing)

	client.apiKey = "test-key"
	client.maximumTiles = 1
	_, err = client.Snapshot(context.Background(), []Tile{
		{Zoom: 14, X: 1, Y: 1},
		{Zoom: 14, X: 2, Y: 1},
	})
	assertProviderCode(t, err, ErrorTileLimit)
}

func TestClientSnapshotHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Snapshot(ctx, []Tile{{Zoom: 14, X: 1, Y: 1}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot error = %v, want context cancellation", err)
	}
}

func TestClientSnapshotRejectsInvalidProviderTile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "invalid-pbf")
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, time.Now)

	_, err := client.Snapshot(context.Background(), []Tile{{Zoom: 14, X: 1, Y: 1}})
	assertProviderCode(t, err, ErrorInvalidTile)
}

func newTestClient(t *testing.T, baseURL string, now func() time.Time) *Client {
	t.Helper()
	client, err := NewClient(Options{
		APIKey:            "test-key",
		BaseURL:           baseURL,
		RequestTimeout:    time.Second,
		CacheTTL:          30 * time.Minute,
		CacheEntries:      256,
		CacheBytes:        16 << 20,
		RequestsPerSecond: 1_000_000,
		MaximumTiles:      32,
		Margin:            0.1,
		Limits: Limits{
			MaximumTileBytes: 1 << 20,
			MaximumFeatures:  100,
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    now,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	return client
}

func assertProviderCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var providerError *Error
	if !errors.As(err, &providerError) || providerError.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}
