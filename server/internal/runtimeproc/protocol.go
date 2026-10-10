// Package runtimeproc provides authenticated local process control and durable operation receipts.
package runtimeproc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ProtocolVersion is the incompatible-contract version of the private transport.
const ProtocolVersion = 1

// Entrypoint bypasses normal CLI profile and account loading.
const Entrypoint = "internal-runtime-service"
const maxBody = 256 << 10
const maxJournal = 8 << 20
const maxOperations = 128

// Scope isolates service ownership across backend, account, profile and daemon.
type Scope struct {
	Backend  string `json:"backend"`
	Account  string `json:"account"`
	Profile  string `json:"profile"`
	DaemonID string `json:"daemon_id"`
	Service  string `json:"service"`
}

// Identity is immutable for the lifetime of a process.
type Identity struct {
	Scope      Scope  `json:"scope"`
	InstanceID string `json:"instance_id"`
	Build      string `json:"build"`
	Protocol   int    `json:"protocol"`
}

// Fence identifies the supervisor and resource revision authorizing a mutation.
type Fence struct {
	SupervisorEpoch uint64 `json:"supervisor_epoch"`
	ResourceEpoch   uint64 `json:"resource_epoch"`
	Revision        uint64 `json:"revision"`
}

// Request carries a bounded operation, identity, deadline and optional domain scope.
// Domain handlers must validate workspace/task/execution authorization in Payload.
type Request struct {
	ReplayEpoch uint64          `json:"replay_epoch"`
	Identity    Identity        `json:"identity"`
	RequestID   string          `json:"request_id"`
	Operation   string          `json:"operation"`
	Deadline    time.Time       `json:"deadline"`
	Fence       Fence           `json:"fence"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// Error is a safe machine-readable transport or domain failure.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Status describes authenticated readiness, not merely a live PID.
type Status struct {
	ReplayEpoch  uint64   `json:"replay_epoch"`
	Identity     Identity `json:"identity"`
	State        string   `json:"state"`
	Fence        Fence    `json:"fence"`
	Capabilities []string `json:"capabilities"`
}

// Receipt records an operation before execution; pending means the result is uncertain.
type Receipt struct {
	RequestID string          `json:"request_id"`
	Digest    string          `json:"digest"`
	State     string          `json:"state"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Error          `json:"error,omitempty"`
	Fence     Fence           `json:"fence"`
}

// Response always binds data to the authenticated process identity.
type Response struct {
	Status  Status   `json:"status"`
	Receipt *Receipt `json:"receipt,omitempty"`
	Error   *Error   `json:"error,omitempty"`
}

// Handler executes a serialized domain mutation. It must honor cancellation and
// own cleanup before returning. Payloads/results must contain no account secrets:
// results are retained in the private journal. External effects are not exactly-once.
type Handler func(context.Context, Request) (json.RawMessage, *Error)

// NewIdentity generates an independent process instance identity.
func NewIdentity(scope Scope, build string) (Identity, error) {
	id, err := randomHex(16)
	if err != nil {
		return Identity{}, err
	}
	identity := Identity{Scope: scope, InstanceID: id, Build: build, Protocol: ProtocolVersion}
	return identity, identity.Validate()
}
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Validate rejects incomplete or incompatible process identities.
func (i Identity) Validate() error {
	for _, v := range []string{i.Scope.Backend, i.Scope.Account, i.Scope.DaemonID, i.Scope.Service, i.InstanceID, i.Build} {
		if v == "" || len(v) > 512 || strings.ContainsAny(v, "\x00\r\n") {
			return errors.New("invalid runtime identity")
		}
	}
	if len(i.Scope.Profile) > 512 || strings.ContainsAny(i.Scope.Profile, "\x00\r\n") {
		return errors.New("invalid runtime profile")
	}
	if i.Protocol != ProtocolVersion {
		return errors.New("incompatible runtime protocol")
	}
	if len(i.InstanceID) != 32 {
		return errors.New("invalid instance identity")
	}
	if _, err := hex.DecodeString(i.InstanceID); err != nil {
		return errors.New("invalid instance identity")
	}
	return nil
}
func validateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("runtime address must be a loopback origin")
	}
	_, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid runtime port")
	}
	return nil
}
