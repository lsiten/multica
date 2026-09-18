//go:build !darwin && !linux

package hostclient

import (
	"context"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
)

func StartInputQualification(context.Context, Config, smokefixture.QualificationScope) (*Client, error) {
	return nil, native.ErrUnsupported
}
