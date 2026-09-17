package main

import (
	"context"
	"os"
	"reflect"
	"testing"
)

func TestLoadAreaGeometrySelectsFeatureByID(t *testing.T) {
	path := t.TempDir() + "/areas.geojson"
	data := []byte(`{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"id":"02"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write area fixture: %v", err)
	}
	geometry, err := loadAreaGeometry(path, "02")
	if err != nil {
		t.Fatalf("loadAreaGeometry returned error: %v", err)
	}
	if string(geometry) != `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}` {
		t.Fatalf("unexpected geometry: %s", geometry)
	}
}

func TestParseZoomsSortsAndDeduplicates(t *testing.T) {
	got, err := parseZooms("16, 14,15,14")
	if err != nil {
		t.Fatalf("parseZooms returned error: %v", err)
	}
	want := []int{14, 15, 16}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseZooms = %v, want %v", got, want)
	}
}

func TestParseZoomsRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "14,no", "23"} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseZooms(value); err == nil {
				t.Fatalf("parseZooms(%q) succeeded, want error", value)
			}
		})
	}
}

func TestTilesAroundObeliscoAtZoom15(t *testing.T) {
	tiles, err := tilesAround(defaultLatitude, defaultLongitude, 1000, 15)
	if err != nil {
		t.Fatalf("tilesAround returned error: %v", err)
	}
	if len(tiles) < 4 || len(tiles) > 16 {
		t.Fatalf("tilesAround returned %d tiles, expected a small local grid", len(tiles))
	}
	for _, tile := range tiles {
		if tile.Z != 15 {
			t.Fatalf("tile zoom = %d, want 15", tile.Z)
		}
	}
}

func TestLoadOrFetchTileUsesReadThroughCache(t *testing.T) {
	directory := t.TempDir()
	tile := tile{X: 1, Y: 2, Z: 14}
	want := []byte("cached-pbf")
	if err := os.WriteFile(tilePath(directory, 0.1, tile), want, 0o600); err != nil {
		t.Fatalf("write cached tile: %v", err)
	}

	got, cacheHit, err := loadOrFetchTile(
		context.Background(),
		nil,
		"unused-key",
		0.1,
		tile,
		directory,
	)
	if err != nil {
		t.Fatalf("loadOrFetchTile returned error: %v", err)
	}
	if !cacheHit {
		t.Fatal("loadOrFetchTile cacheHit = false, want true")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadOrFetchTile data = %q, want %q", got, want)
	}
}

func TestRedactSecretRemovesPlainAndEscapedKey(t *testing.T) {
	const key = "secret+key/value"
	message := "plain=secret+key/value encoded=secret%2Bkey%2Fvalue"
	got := redactSecret(message, key)
	if got != "plain=[REDACTED] encoded=[REDACTED]" {
		t.Fatalf("redactSecret = %q", got)
	}
}

func TestParseOptionsUsesSafeDefaults(t *testing.T) {
	got, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions returned error: %v", err)
	}
	if got.radius != defaultRadius || got.margin != defaultMargin {
		t.Fatalf("unexpected defaults: %#v", got)
	}
	if !reflect.DeepEqual(got.zooms, []int{14, 15, 16}) {
		t.Fatalf("default zooms = %v", got.zooms)
	}
}
