package app

import (
	"io"
	"log/slog"
	"testing"
)

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
