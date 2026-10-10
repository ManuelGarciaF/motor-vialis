package gcba

import "fmt"

// ErrorCode identifies failures callers may handle without inspecting text.
type ErrorCode string

const (
	ErrorCredentialsMissing ErrorCode = "credentials_missing"
	ErrorUnauthorized       ErrorCode = "unauthorized"
	ErrorRateLimited        ErrorCode = "rate_limited"
	ErrorUnavailable        ErrorCode = "unavailable"
	ErrorResponseTooLarge   ErrorCode = "response_too_large"
	ErrorInvalidResponse    ErrorCode = "invalid_response"
)

// Error is a safe provider error. It never contains an authenticated URL.
type Error struct {
	Code       ErrorCode
	Message    string
	StatusCode int
	Cause      error
}

func (err *Error) Error() string {
	if err.StatusCode != 0 {
		return fmt.Sprintf("%s: %s (HTTP %d)", err.Code, err.Message, err.StatusCode)
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Message)
}

func (err *Error) Unwrap() error { return err.Cause }
