package applicationgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const runtimeLeaseTTL = 45 * time.Second

type cluster struct {
	redis     redis.UniversalClient
	origin    string
	secret    []byte
	startOnce sync.Once
	startErr  error
	done      chan struct{}
}
type owner struct {
	Origin string `json:"origin"`
	Epoch  string `json:"epoch"`
}

// NewClusterHub routes runtime streams across replicas through expiring Redis ownership leases.
func NewClusterHub(client redis.UniversalClient, origin string, secret []byte) (*Hub, error) {
	address, err := url.Parse(origin)
	if err != nil || address.Host == "" || address.User != nil || (address.Scheme != "http" && address.Scheme != "https") || address.RawQuery != "" || address.Fragment != "" || (address.Path != "" && address.Path != "/") {
		return nil, errors.New("application peer origin must be an absolute HTTP(S) origin")
	}
	if client == nil || len(secret) < 16 {
		return nil, errors.New("application clustering requires Redis and a signing secret")
	}
	hub := NewHub()
	hub.cluster = &cluster{redis: client, origin: strings.TrimRight(origin, "/"), secret: secret, done: make(chan struct{})}
	return hub, nil
}

func runtimeOwnerKey(runtimeID string) string { return "multica:applications:runtime:" + runtimeID }
func streamOwnerKey(streamID string) string   { return "multica:applications:stream:" + streamID }

func (c *cluster) runtimeOwner(ctx context.Context, runtimeID string) (owner, error) {
	value, err := c.redis.Get(ctx, runtimeOwnerKey(runtimeID)).Result()
	if errors.Is(err, redis.Nil) {
		return owner{}, ErrOffline
	}
	if err != nil {
		return owner{}, err
	}
	var current owner
	if err := json.Unmarshal([]byte(value), &current); err != nil || current.Origin == "" || current.Epoch == "" {
		return owner{}, ErrOffline
	}
	return current, nil
}

func (c *cluster) register(ctx context.Context, runtimeID, epoch string) error {
	value, err := json.Marshal(owner{Origin: c.origin, Epoch: epoch})
	if err != nil {
		return err
	}
	return c.redis.Set(ctx, runtimeOwnerKey(runtimeID), value, runtimeLeaseTTL).Err()
}

func (c *cluster) renew(ctx context.Context, runtimeID, epoch string) error {
	value, err := json.Marshal(owner{Origin: c.origin, Epoch: epoch})
	if err != nil {
		return err
	}
	renewed, err := c.redis.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then redis.call('PEXPIRE',KEYS[1],ARGV[2]);return 1 end;return 0`, []string{runtimeOwnerKey(runtimeID)}, string(value), runtimeLeaseTTL.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if renewed != 1 {
		return ErrOffline
	}
	return nil
}

func (c *cluster) release(ctx context.Context, runtimeID, epoch string) error {
	value, err := json.Marshal(owner{Origin: c.origin, Epoch: epoch})
	if err != nil {
		return err
	}
	return c.redis.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end;return 0`, []string{runtimeOwnerKey(runtimeID)}, string(value)).Err()
}

// Available includes a live remote owner instead of equating another replica with an offline runtime.
func (h *Hub) Available(ctx context.Context, runtimeID string) (bool, error) {
	if h.cluster == nil {
		return h.Connected(runtimeID), nil
	}
	current, err := h.cluster.runtimeOwner(ctx, runtimeID)
	if errors.Is(err, ErrOffline) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current.Origin != h.cluster.origin {
		return true, nil
	}
	h.mu.Lock()
	local := h.controls[runtimeID]
	ready := local != nil && local.epoch == current.Epoch
	h.mu.Unlock()
	return ready, nil
}
