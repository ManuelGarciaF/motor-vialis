package detour

import "fmt"

// ErrorCode is a stable machine-readable detour failure code.
type ErrorCode string

const (
	ErrorInvalidCut                  ErrorCode = "invalid_cut"
	ErrorCutOutsideGraph             ErrorCode = "cut_outside_graph"
	ErrorCutNotMatched               ErrorCode = "cut_not_matched"
	ErrorRouteNotAffected            ErrorCode = "route_not_affected"
	ErrorAnchorNotMatched            ErrorCode = "anchor_not_matched"
	ErrorRequiredStopNotMatched      ErrorCode = "required_stop_not_matched"
	ErrorNoDetourWithinSearchArea    ErrorCode = "no_detour_within_search_area"
	ErrorTrafficUnavailable          ErrorCode = "traffic_unavailable"
	ErrorTrafficStale                ErrorCode = "traffic_stale"
	ErrorTrafficCoverageInsufficient ErrorCode = "traffic_coverage_insufficient"
	ErrorTrafficTileLimitExceeded    ErrorCode = "traffic_tile_limit_exceeded"
)

// ErrorKind distinguishes invalid input, a legitimate lack of a solution, and
// an unavailable dependency without relying on HTTP status codes.
type ErrorKind string

const (
	ErrorKindInvalidInput ErrorKind = "invalid_input"
	ErrorKindNoSolution   ErrorKind = "no_solution"
	ErrorKindDependency   ErrorKind = "dependency_failure"
)

// Error is a typed domain failure. Cause is for internal wrapping and must not
// be serialized directly because provider errors may contain sensitive data.
type Error struct {
	Code    ErrorCode
	Kind    ErrorKind
	Message string
	Cause   error
}

func (err *Error) Error() string {
	if err.Message == "" {
		return string(err.Code)
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Message)
}

func (err *Error) Unwrap() error { return err.Cause }

// ValidationError reports an invalid detour field that is not specific to the
// cut geometry, such as an unknown selection criterion.
type ValidationError struct {
	Field   string
	Message string
}

func (err *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

func invalidCut(message string) error {
	return &Error{
		Code:    ErrorInvalidCut,
		Kind:    ErrorKindInvalidInput,
		Message: message,
	}
}
