package daemon

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type applicationSourceEnvelope struct {
	InstanceID      string                   `json:"instance_id"`
	GrantGeneration int64                    `json:"grant_generation"`
	Deadline        time.Time                `json:"deadline"`
	Source          applicationSourceRequest `json:"source"`
}
type applicationSourceCallback struct {
	cancel                     context.CancelFunc
	workers                    sync.WaitGroup
	closed                     bool
	daemon                     *Daemon
	instanceID, token, address string
	server                     *http.Server
	done                       chan struct{}
	mu                         sync.RWMutex
	grants                     map[string]protocol.ApplicationServiceGrantResponse
	admission                  chan struct{}
}

func validateApplicationCallbackAddress(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("application source endpoint is not private loopback")
	}
	return nil
}
func newApplicationSourceCallback(d *Daemon, instanceID, token string) (*applicationSourceCallback, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c := &applicationSourceCallback{daemon: d, instanceID: instanceID, token: token, address: "http://" + listener.Addr().String(), done: make(chan struct{}), grants: map[string]protocol.ApplicationServiceGrantResponse{}, admission: make(chan struct{}, 4)}
	life, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.server = &http.Server{BaseContext: func(net.Listener) context.Context { return life }, Handler: http.HandlerFunc(c.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	go func() { defer close(c.done); _ = c.server.Serve(listener) }()
	return c, nil
}
func (c *applicationSourceCallback) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	_ = c.server.Close()
	<-c.done
	c.workers.Wait()
}
func (c *applicationSourceCallback) serve(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		http.Error(w, "closed", http.StatusServiceUnavailable)
		return
	}
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()
	if r.Method != http.MethodPost || r.URL.Path != "/source" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var input applicationSourceEnvelope
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || input.InstanceID != c.instanceID || input.Deadline.Before(time.Now()) || input.Deadline.After(time.Now().Add(10*time.Minute)) {
		http.Error(w, "invalid source request", http.StatusBadRequest)
		return
	}
	c.mu.RLock()
	grant, ok := c.grants[input.Source.RuntimeID]
	c.mu.RUnlock()
	if !ok || grant.Generation != input.GrantGeneration || grant.WorkspaceID != input.Source.WorkspaceID || !grant.ExpiresAt.After(time.Now()) || !c.daemon.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: input.Source.WorkspaceID, RuntimeID: input.Source.RuntimeID}) {
		http.Error(w, "source authority changed", http.StatusForbidden)
		return
	}
	select {
	case c.admission <- struct{}{}:
		defer func() { <-c.admission }()
	default:
		http.Error(w, "source capacity exhausted", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), input.Deadline)
	defer cancel()
	source := input.Source
	config := protocol.DefaultApplicationConfig()
	config.Mode = "managed"
	config.ResourceID = source.ResourceID
	config.Ref = source.Ref
	config.WorkDir = source.WorkDir
	config.LocalEnv = source.LocalEnv
	config.Command = []string{"source-validation"}
	config.Health.Kind = "none"

	command := protocol.ApplicationControlCommand{WorkspaceID: source.WorkspaceID, RuntimeID: source.RuntimeID, ApplicationID: source.ApplicationID, InstanceID: source.InstanceID, Revision: source.Revision, ResourceType: source.ResourceType, ResourceRef: source.ResourceRef, Config: config}
	prepared, err := c.daemon.prepareApplicationSource(ctx, command)
	if err != nil {
		http.Error(w, "application source preparation failed", http.StatusConflict)
		return
	}
	c.mu.RLock()
	current, ok := c.grants[source.RuntimeID]
	c.mu.RUnlock()
	if !ok || current.Generation != grant.Generation || !current.ExpiresAt.After(time.Now()) {
		http.Error(w, "source authority changed", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(prepared)
}
func (s *applicationProcessService) prepareSource(ctx context.Context, source applicationSourceRequest) (applicationPreparedSource, error) {
	s.mu.RLock()
	cohort := s.cohorts[source.RuntimeID]
	address := s.sourceAddress
	s.mu.RUnlock()
	if cohort == nil {
		return applicationPreparedSource{}, errApplicationRuntimeUnavailable
	}
	deadline := time.Now().Add(10 * time.Minute)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	input := applicationSourceEnvelope{InstanceID: s.bootstrap.Identity.InstanceID, GrantGeneration: cohort.client.grant.Generation, Deadline: deadline, Source: source}
	raw, err := json.Marshal(input)
	if err != nil {
		return applicationPreparedSource{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+"/source", bytes.NewReader(raw))
	if err != nil {
		return applicationPreparedSource{}, err
	}
	request.Header.Set("Authorization", "Bearer "+s.bootstrap.Token)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return applicationPreparedSource{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return applicationPreparedSource{}, errors.New("application source callback rejected")
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return applicationPreparedSource{}, errors.New("application source response exceeds limit")
	}
	var result applicationPreparedSource
	if json.Unmarshal(raw, &result) != nil || strings.IndexByte(result.Root, 0) >= 0 {
		return result, errors.New("invalid application source response")
	}
	return result, nil
}
