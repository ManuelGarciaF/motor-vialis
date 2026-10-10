package bunching

import (
	"context"
	"time"
)

// StoreResult counts outcomes after resolving provider identities. Unmapped
// rows (including destinations absent from the current GTFS) are ignored.
type StoreResult struct {
	CaptureID                                 int64
	Inserted, Unmapped, OutsideRetention      int
	BunchingProcessed, BunchingSkipped        bool
	EventsOpened, EventsUpdated, EventsClosed int
}

// MaintenanceResult describes daily UTC partitions created and dropped.
type MaintenanceResult struct{ Created, Dropped []string }

// Repository persists captures without depending on HTTP or a provider client.
type Repository interface {
	StoreCapture(context.Context, Capture) (StoreResult, error)
	MaintainPartitions(context.Context, time.Time) (MaintenanceResult, error)
}
