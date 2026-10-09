package gcba

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type failingTransport struct{}

func (failingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("transport failed for %s", request.URL.String())
}

func TestCaptureDoesNotExposeCredentialsInErrorChain(t *testing.T) {
	options := testOptions("http://example.test")
	options.HTTPClient = &http.Client{Transport: failingTransport{}}
	options.MaximumAttempts = 1
	client, err := NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Capture(context.Background())
	if err == nil {
		t.Fatal("expected transport error")
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if text := cause.Error(); strings.Contains(text, "private") || strings.Contains(text, "client_secret") || strings.Contains(text, "example.test") {
			t.Fatal("authenticated URL leaked")
		}
	}
}
