package gcba

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
)

// Options sets explicit defensive limits. BaseURL and Now support offline tests.
type Options struct {
	ClientID, ClientSecret, BaseURL               string
	HTTPClient                                    *http.Client
	RequestTimeout, MaximumAge, MaximumFutureSkew time.Duration
	InitialBackoff, MaximumBackoff                time.Duration
	MaximumBodyBytes                              int64
	MaximumAttempts                               int
	Now                                           func() time.Time
}

// Client fetches one validated capture at a time, without a polling loop.
type Client struct {
	options    Options
	httpClient *http.Client
	gate       chan struct{}
}

func NewClient(options Options) (*Client, error) {
	if options.ClientID == "" || options.ClientSecret == "" {
		return nil, &Error{Code: ErrorCredentialsMissing, Message: "GCBA credentials are required"}
	}
	if options.BaseURL == "" {
		options.BaseURL = "https://apitransporte.buenosaires.gob.ar"
	}
	u, err := url.Parse(options.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid GCBA base URL")
	}
	if options.RequestTimeout <= 0 || options.MaximumAge <= 0 || options.MaximumFutureSkew < 0 || options.MaximumBodyBytes <= 0 || options.MaximumAttempts < 1 || options.InitialBackoff <= 0 || options.MaximumBackoff < options.InitialBackoff {
		return nil, fmt.Errorf("invalid GCBA limits")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	client := &http.Client{}
	if options.HTTPClient != nil {
		*client = *options.HTTPClient
	}
	client.Timeout = options.RequestTimeout
	// Credentials must never follow a redirect to a different origin.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{options: options, httpClient: client, gate: make(chan struct{}, 1)}, nil
}

// Capture serializes requests and retries only transport errors, 429 and 5xx.
// Retry-After is a minimum wait, even if greater than MaximumBackoff.
func (client *Client) Capture(ctx context.Context) (bunching.Capture, error) {
	select {
	case client.gate <- struct{}{}:
		defer func() { <-client.gate }()
	case <-ctx.Done():
		return bunching.Capture{}, ctx.Err()
	}
	delay := client.options.InitialBackoff
	for attempt := 1; ; attempt++ {
		capture, retryAfter, err := client.fetch(ctx)
		if err == nil {
			return capture, nil
		}
		if ctx.Err() != nil {
			return bunching.Capture{}, ctx.Err()
		}
		var providerError *Error
		retryable := errors.As(err, &providerError) && (providerError.Code == ErrorRateLimited || providerError.Code == ErrorUnavailable && (providerError.StatusCode == 0 || providerError.StatusCode >= 500))
		if !retryable || attempt >= client.options.MaximumAttempts {
			return bunching.Capture{}, err
		}
		wait := delay
		if retryAfter > wait {
			wait = retryAfter
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return bunching.Capture{}, ctx.Err()
		case <-timer.C:
		}
		if delay >= client.options.MaximumBackoff/2 {
			delay = client.options.MaximumBackoff
		} else {
			delay *= 2
		}
	}
}

func (client *Client) fetch(ctx context.Context) (bunching.Capture, time.Duration, error) {
	endpoint, _ := url.Parse(strings.TrimRight(client.options.BaseURL, "/") + "/colectivos/vehiclePositionsSimple")
	query := endpoint.Query()
	query.Set("client_id", client.options.ClientID)
	query.Set("client_secret", client.options.ClientSecret)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return bunching.Capture{}, 0, &Error{Code: ErrorUnavailable, Message: "cannot create GCBA request"}
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		// Do not return url.Error or transport text: either may contain credentials.
		var cause error
		if ctx.Err() != nil {
			cause = ctx.Err()
		} else if errors.Is(err, context.DeadlineExceeded) {
			cause = context.DeadlineExceeded
		}
		return bunching.Capture{}, 0, &Error{Code: ErrorUnavailable, Message: "GCBA request failed", Cause: cause}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		code := ErrorUnavailable
		if response.StatusCode == 401 || response.StatusCode == 403 {
			code = ErrorUnauthorized
		}
		if response.StatusCode == 429 {
			code = ErrorRateLimited
		}
		return bunching.Capture{}, parseRetryAfter(response.Header.Get("Retry-After"), client.options.Now()), &Error{Code: code, Message: "GCBA returned an unsuccessful response", StatusCode: response.StatusCode}
	}
	if response.ContentLength > client.options.MaximumBodyBytes {
		return bunching.Capture{}, 0, &Error{Code: ErrorResponseTooLarge, Message: "GCBA response exceeds body limit"}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.options.MaximumBodyBytes+1))
	if err != nil {
		return bunching.Capture{}, 0, &Error{Code: ErrorUnavailable, Message: "cannot read GCBA response"}
	}
	if int64(len(body)) > client.options.MaximumBodyBytes {
		return bunching.Capture{}, 0, &Error{Code: ErrorResponseTooLarge, Message: "GCBA response exceeds body limit"}
	}
	capture, err := decode(body, client.options.Now().UTC(), client.options.MaximumAge, client.options.MaximumFutureSkew)
	return capture, 0, err
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/int64(time.Second)) {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}
