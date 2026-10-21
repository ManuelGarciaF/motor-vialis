package bottlenecks

import (
	"context"
	"errors"
)

var (
	ErrProducerModeConflict        = errors.New("measurement model uses another processing mode")
	ErrProducerSourceIncomplete    = errors.New("canonical producer source is incomplete")
	ErrProducerGeometryUnavailable = errors.New("exact producer geometry is unavailable")
)

// ProducerProgress describes only committed work. Processed=false means no new
// capture; it is not a failed or empty provider capture and never counts absence.
type ProducerProgress struct {
	Processed                                                 bool
	Checkpoint                                                int64
	Vehicles, ClosedVehicles                                  int
	Passages, Incomplete, LateObservations, Duplicates        int
	SpatiallyUnique, TemporallyResolved, Ambiguous, Unmatched int
	Rejections                                                map[Rejection]int
}

// ProducerRepository owns source reading and the transaction confirming receipt
// state, final evidence and checkpoint together. It does not activate a model.
type ProducerRepository interface {
	ProcessNext(context.Context, string) (ProducerProgress, error)
	LoadProducerStates(context.Context, string) (ProcessingRun, []ProducerState, error)
}
