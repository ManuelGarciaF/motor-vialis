package config

import (
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
)

const (
	// defaultAlignmentToleranceMeters is deliberately far wider than the 20 m
	// route.Validate demands: it decides whether a stored path endpoint is
	// recognisably the same place as its stop, not whether a caller sent
	// coherent geometry. Raise it only if the GTFS feed places shape
	// boundaries further from its stops.
	defaultAlignmentToleranceMeters = 250.0

	defaultLinesPageSize = 50
	// maximumLinesPageSize bounds one response so listing the AMBA feed cannot
	// be turned into a full table dump by a single request.
	maximumLinesPageSize = 200
)

// DefaultLinesPolicy returns the default policy used when exporting stored
// lines.
func DefaultLinesPolicy() lines.Policy {
	return lines.Policy{
		AlignmentToleranceMeters: defaultAlignmentToleranceMeters,
		DefaultPageSize:          defaultLinesPageSize,
		MaximumPageSize:          maximumLinesPageSize,
	}
}

func linesPolicyFromEnv() (lines.Policy, error) {
	policy := DefaultLinesPolicy()

	tolerance, err := positiveFloatFromEnv(
		"LINES_ALIGNMENT_TOLERANCE_METERS",
		policy.AlignmentToleranceMeters,
	)
	if err != nil {
		return lines.Policy{}, err
	}
	defaultPageSize, err := positiveIntFromEnv(
		"LINES_DEFAULT_PAGE_SIZE",
		policy.DefaultPageSize,
	)
	if err != nil {
		return lines.Policy{}, err
	}
	maximumPageSize, err := positiveIntFromEnv(
		"LINES_MAXIMUM_PAGE_SIZE",
		policy.MaximumPageSize,
	)
	if err != nil {
		return lines.Policy{}, err
	}

	if defaultPageSize > maximumPageSize {
		return lines.Policy{}, fmt.Errorf(
			"LINES_DEFAULT_PAGE_SIZE must not exceed LINES_MAXIMUM_PAGE_SIZE: %d > %d",
			defaultPageSize,
			maximumPageSize,
		)
	}

	policy.AlignmentToleranceMeters = tolerance
	policy.DefaultPageSize = defaultPageSize
	policy.MaximumPageSize = maximumPageSize
	return policy, nil
}
