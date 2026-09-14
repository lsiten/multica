package vscreen

import (
	"context"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Display is a native-verified display incarnation, independent of viewers.
type Display struct {
	Resource  ResourceKey
	Epoch     Epoch
	DisplayID uint32
}

// Action is the shared checked input contract.
type Action = protocol.VscreenActionRequest

// ActionResult distinguishes delivery from verified effects and uncertain completion.
type ActionResult struct {
	Epoch   Epoch
	Outcome protocol.VscreenActionOutcome
}

// Driver is implemented by the authenticated native-host adapter. Act must recheck
// window/process ownership immediately before dispatch. Quiesce returns nil only
// after all native operations stop; a timeout is not proof of quiescence. Native
// AppClaim locks remain held through that barrier, including parent disconnect.
type Driver interface {
	Ensure(context.Context, ResourceKey) (Display, error)
	Act(context.Context, Action) (ActionResult, error)
	Quiesce(context.Context, ResourceKey) error
	Dispose(context.Context, ResourceKey) error
}
