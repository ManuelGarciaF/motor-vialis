package gcba

import (
	"context"
	"os"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
)

func TestLiveCapture(t *testing.T) {
	if os.Getenv("GCBA_LIVE_TEST") != "1" {
		t.Skip("set GCBA_LIVE_TEST=1 to call GCBA")
	}
	cfg := config.FromEnv()
	client, err := NewClient(Options{
		ClientID: cfg.GCBATransportClientID, ClientSecret: cfg.GCBATransportClientSecret,
		RequestTimeout: config.GCBARequestTimeout, MaximumBodyBytes: config.GCBAMaximumBodyBytes,
		MaximumAge: config.GCBAMaximumPositionAge, MaximumFutureSkew: config.GCBAMaximumFutureSkew,
		MaximumAttempts: config.GCBAMaximumAttempts, InitialBackoff: config.GCBAInitialBackoff,
		MaximumBackoff: config.GCBAMaximumBackoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := client.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("receivedAt=%s rows=%d valid=%d invalid=%d duplicates=%d stale=%d", capture.ReceivedAt, capture.Stats.TotalRows, capture.Stats.ValidRows, capture.Stats.InvalidRows, capture.Stats.DuplicateRows, capture.Stats.StaleRows)
}
