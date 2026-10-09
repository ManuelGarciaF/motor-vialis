package gcba

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var testNow = time.Unix(1735689600, 0).UTC()

func testOptions(baseURL string) Options {
	return Options{ClientID: "private-id", ClientSecret: "private-secret", BaseURL: baseURL, RequestTimeout: time.Second, MaximumBodyBytes: 1 << 20, MaximumAge: 5 * time.Minute, MaximumFutureSkew: time.Minute, MaximumAttempts: 3, InitialBackoff: time.Millisecond, MaximumBackoff: 4 * time.Millisecond, Now: func() time.Time { return testNow }}
}

func TestCaptureFixtures(t *testing.T) {
	for _, test := range []struct {
		name           string
		valid, invalid int
	}{
		{"normal", 2, 0}, {"empty", 0, 0}, {"invalid-fields", 0, 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := os.ReadFile("testdata/vehicle-positions-" + test.name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/colectivos/vehiclePositionsSimple" || r.URL.Query().Get("client_id") != "private-id" || r.URL.Query().Get("client_secret") != "private-secret" {
					t.Error("wrong endpoint or authentication")
				}
				w.Write(body)
			}))
			defer server.Close()
			client, err := NewClient(testOptions(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			capture, err := client.Capture(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if capture.Stats.ValidRows != test.valid || capture.Stats.InvalidRows != test.invalid {
				t.Fatalf("stats: %+v", capture.Stats)
			}
			if len(capture.Positions) > 0 && (!capture.Positions[0].Fresh || capture.Positions[0].ProviderTripID != "trip-001") {
				t.Fatalf("position: %+v", capture.Positions[0])
			}
		})
	}
}

func TestCaptureResponseFailures(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		code       ErrorCode
		attempts   int
	}{
		{"unauthorized", "private-secret", 401, ErrorUnauthorized, 1},
		{"forbidden", "", 403, ErrorUnauthorized, 1},
		{"rate-limit", "", 429, ErrorRateLimited, 3},
		{"server-error", "", 500, ErrorUnavailable, 3},
		{"invalid-json", "[", 200, ErrorInvalidResponse, 1},
		{"object", "{}", 200, ErrorInvalidResponse, 1},
		{"null", "null", 200, ErrorInvalidResponse, 1},
		{"trailing", "[] {}", 200, ErrorInvalidResponse, 1},
		{"trailing-invalid", "[] garbage", 200, ErrorInvalidResponse, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := NewClient(testOptions(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Capture(context.Background())
			var provider *Error
			if !errors.As(err, &provider) || provider.Code != test.code {
				t.Fatalf("error: %v", err)
			}
			if calls.Load() != int32(test.attempts) {
				t.Fatalf("calls: %d", calls.Load())
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestCaptureRetriesAndRecovers(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, "[]")
	}))
	defer server.Close()
	client, _ := NewClient(testOptions(server.URL))
	capture, err := client.Capture(context.Background())
	if err != nil || capture.Stats.TotalRows != 0 || calls.Load() != 3 {
		t.Fatalf("result: %+v %v", capture, err)
	}
}

func TestCaptureBodyLimit(t *testing.T) {
	for _, declared := range []bool{true, false} {
		t.Run(fmt.Sprint(declared), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if declared {
					w.Header().Set("Content-Length", "100")
				} else {
					w.(http.Flusher).Flush()
				}
				io.WriteString(w, strings.Repeat(" ", 100))
			}))
			defer server.Close()
			options := testOptions(server.URL)
			options.MaximumBodyBytes = 20
			client, _ := NewClient(options)
			_, err := client.Capture(context.Background())
			var provider *Error
			if !errors.As(err, &provider) || provider.Code != ErrorResponseTooLarge {
				t.Fatalf("error: %v", err)
			}
		})
	}
}

func TestCaptureTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	options := testOptions(server.URL)
	options.RequestTimeout = 10 * time.Millisecond
	options.MaximumAttempts = 1
	client, _ := NewClient(options)
	_, err := client.Capture(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", err), "private") {
		t.Fatal("credential leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Capture(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error: %v", err)
	}
}

func TestCaptureHonorsRetryAfterCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Retry-After", "60"); w.WriteHeader(429) }))
	defer server.Close()
	client, _ := NewClient(testOptions(server.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Capture(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error: %v", err)
	}
	if got := parseRetryAfter(testNow.Add(time.Minute).Format(http.TimeFormat), testNow); got != time.Minute {
		t.Fatalf("Retry-After: %v", got)
	}
}

func TestCaptureRejectsRedirect(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { followed.Add(1); io.WriteString(w, "[]") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer server.Close()
	client, _ := NewClient(testOptions(server.URL))
	_, err := client.Capture(context.Background())
	if err == nil || followed.Load() != 0 {
		t.Fatal("redirect followed")
	}
}

func TestNewClientValidation(t *testing.T) {
	for _, mutate := range []func(*Options){func(o *Options) { o.ClientID = "" }, func(o *Options) { o.ClientSecret = "" }, func(o *Options) { o.BaseURL = "bad" }, func(o *Options) { o.MaximumAttempts = 0 }, func(o *Options) { o.RequestTimeout = 0 }} {
		options := testOptions("http://example.test")
		mutate(&options)
		if _, err := NewClient(options); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}
