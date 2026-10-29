package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
)

func TestDemoTrafficProviderAllowsMissingKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewDetourTrafficProvider("", logger, false); err == nil {
		t.Fatal("live mode accepted missing credentials")
	}
	provider, err := NewDetourTrafficProvider("", logger, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Snapshot(context.Background(), nil)
	var e *traffic.Error
	if !errors.As(err, &e) || e.Code != traffic.ErrorAPIKeyMissing {
		t.Fatalf("missing credentials not reported: %v", err)
	}
}

func TestNewTomTomTrafficClientRequiresAPIKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewTomTomTrafficClient("", logger); err == nil {
		t.Fatal("NewTomTomTrafficClient accepted an empty API key")
	}
	if _, err := NewTomTomTrafficClient("   ", logger); err == nil {
		t.Fatal("NewTomTomTrafficClient accepted a blank API key")
	}
	if _, err := NewTomTomTrafficClient("test-key", logger); err != nil {
		t.Fatalf("NewTomTomTrafficClient returned error: %v", err)
	}
}
