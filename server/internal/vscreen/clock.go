package vscreen

import "time"

// Clock supplies monotonic time; implementations must retain time.Time monotonic readings.
type Clock interface{ Now() time.Time }

// SystemClock uses Go's monotonic process clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

const (
	LeaseHeartbeat   = 5 * time.Second
	LeaseTTL         = 15 * time.Second
	TransactionLimit = 120 * time.Second
	ActionDeadline   = 3 * time.Second
)
