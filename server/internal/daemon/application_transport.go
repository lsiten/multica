package daemon

import (
	"context"
	"errors"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// applicationTransport is runtime scoped. The process implementation contains
// only an application service grant; the default owner uses its existing client.
type applicationTransport interface {
	Sync(context.Context) ([]protocol.ApplicationRuntimeInstance, error)
	Claim(context.Context) ([]protocol.ApplicationClaim, error)
	Observe(context.Context, protocol.ApplicationObservation) error
	Complete(context.Context, protocol.ApplicationClaim, protocol.ApplicationStepResult) error
	RenewLease(context.Context, protocol.ApplicationClaim) error
	Tunnel(context.Context, string) (*websocket.Conn, error)
}

type localApplicationTransport struct {
	client              *Client
	runtimeID, daemonID string
}

func (a localApplicationTransport) Sync(ctx context.Context) ([]protocol.ApplicationRuntimeInstance, error) {
	return a.client.syncApplications(ctx, a.runtimeID, a.daemonID)
}
func (a localApplicationTransport) Claim(ctx context.Context) ([]protocol.ApplicationClaim, error) {
	return a.client.claimApplications(ctx, a.runtimeID, a.daemonID)
}
func (a localApplicationTransport) Observe(ctx context.Context, o protocol.ApplicationObservation) error {
	return a.client.observeApplication(ctx, a.runtimeID, a.daemonID, o)
}
func (a localApplicationTransport) Complete(ctx context.Context, c protocol.ApplicationClaim, r protocol.ApplicationStepResult) error {
	return a.client.completeApplication(ctx, a.runtimeID, a.daemonID, c, r)
}
func (a localApplicationTransport) RenewLease(ctx context.Context, c protocol.ApplicationClaim) error {
	return a.client.renewApplicationLease(ctx, a.runtimeID, a.daemonID, c)
}
func (a localApplicationTransport) Tunnel(ctx context.Context, k string) (*websocket.Conn, error) {
	return a.client.connectApplicationTunnel(ctx, a.runtimeID, k, a.daemonID)
}
func (d *Daemon) applicationTransportFor(runtimeID string) applicationTransport {
	if d.applicationTransport != nil {
		return d.applicationTransport(runtimeID)
	}
	return localApplicationTransport{client: d.client, runtimeID: runtimeID, daemonID: d.cfg.DaemonID}
}

type unavailableApplicationTransport struct{}

func (unavailableApplicationTransport) Sync(context.Context) ([]protocol.ApplicationRuntimeInstance, error) {
	return nil, errApplicationRuntimeUnavailable
}
func (unavailableApplicationTransport) Claim(context.Context) ([]protocol.ApplicationClaim, error) {
	return nil, errApplicationRuntimeUnavailable
}
func (unavailableApplicationTransport) Observe(context.Context, protocol.ApplicationObservation) error {
	return errApplicationRuntimeUnavailable
}
func (unavailableApplicationTransport) Complete(context.Context, protocol.ApplicationClaim, protocol.ApplicationStepResult) error {
	return errApplicationRuntimeUnavailable
}
func (unavailableApplicationTransport) RenewLease(context.Context, protocol.ApplicationClaim) error {
	return errApplicationRuntimeUnavailable
}
func (unavailableApplicationTransport) Tunnel(context.Context, string) (*websocket.Conn, error) {
	return nil, errApplicationRuntimeUnavailable
}

var errApplicationRuntimeUnavailable = errors.New("application runtime authority unavailable")
