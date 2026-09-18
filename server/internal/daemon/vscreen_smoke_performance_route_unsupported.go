//go:build !darwin

package daemon

import (
	"context"
	"github.com/multica-ai/multica/server/internal/mirror"
)

func currentPerformanceRoute(context.Context, mirror.SelectedICEPair) performanceRouteEvidence {
	return performanceRouteEvidence{Kind: "unknown", Reason: "host_route_reader_unsupported", Method: "unavailable"}
}
