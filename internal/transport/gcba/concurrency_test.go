package gcba

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCaptureSerializesConcurrentCalls(t *testing.T) {
	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if active.Add(1) != 1 {
			t.Error("overlapping provider requests")
		}
		defer active.Add(-1)
		time.Sleep(5 * time.Millisecond)
		io.WriteString(w, "[]")
	}))
	defer server.Close()
	client, err := NewClient(testOptions(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			if _, err := client.Capture(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
}
