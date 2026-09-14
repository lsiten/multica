//go:build !darwin && !linux

package hostclient

import (
	"context"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
)

// Start fails closed on platforms without the inherited Unix transport.
func Start(ctx context.Context, config Config) (*Client, error) {
	return nil, native.ErrUnsupported
}
