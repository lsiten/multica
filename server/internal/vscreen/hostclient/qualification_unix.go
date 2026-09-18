//go:build darwin || linux

package hostclient

import (
	"context"
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"os"
)

// StartInputQualification is isolated from normal Config and native RPC. The host
// independently verifies this one disposable fixture before granting experimental input.
func StartInputQualification(ctx context.Context, config Config, scope smokefixture.QualificationScope) (*Client, error) {
	if err := scope.Validate(os.Getpid(), config.Executable); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(scope)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4096 {
		return nil, native.ErrProtocol
	}
	config.Media = true
	config.AppControl = true
	return start(ctx, config, raw)
}
