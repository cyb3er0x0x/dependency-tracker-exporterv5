package collector

import (
	"time"

	"github.com/cyb3er0x0x/dependency-tracker-exporterv5/internal/dtrack"
)

// Snapshot is an immutable point-in-time view of the Dependency-Track portfolio.
// The background Collector produces one per successful refresh; the exporter
// renders whichever Snapshot is currently in the Store.
type Snapshot struct {
	CollectedAt time.Time
	Duration    time.Duration

	Portfolio  dtrack.Metrics
	Projects   []dtrack.Project
	Violations []dtrack.PolicyViolation

	// ServerVersion is the Dependency-Track version the data came from.
	ServerVersion string
}

// Age returns how long ago the snapshot was collected.
func (s *Snapshot) Age() time.Duration { return time.Since(s.CollectedAt) }
