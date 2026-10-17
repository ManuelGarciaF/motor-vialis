package realtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/transport/gcba"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestErrorClass(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"unauthorized", &gcba.Error{Code: gcba.ErrorUnauthorized, StatusCode: 401}, "gcba_unauthorized"},
		{"wrapped provider", fmt.Errorf("capture: %w", &gcba.Error{Code: gcba.ErrorInvalidResponse}), "gcba_invalid_response"},
		{"canceled", context.Canceled, "canceled"},
		{"provider timeout", &gcba.Error{Code: gcba.ErrorUnavailable, Cause: context.DeadlineExceeded}, "deadline_exceeded"},
		{"postgres", &pgconn.PgError{Code: "23505"}, "postgres_23505"},
		{"unknown", errors.New("secret URL"), "operation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ErrorClass(tt.err); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
