package native

import (
	"context"
	"strconv"
	"time"
)

func (h *appHost) reply(r Request, out *AppResponse, err error) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if e := h.conn.SetWriteDeadline(time.Now().Add(time.Second)); e != nil {
		return e
	}
	return WriteMessage(h.conn, Response{Version: ProtocolVersion, Build: h.build, ID: r.ID, Epoch: r.Epoch, App: out, Error: appError(err)})
}
func (h *appHost) serve() {
	defer close(h.done)
	defer h.conn.Close()
	defer func() {
		h.mu.Lock()
		h.closed = true
		for k := range h.leases {
			h.fenceLocked(k)
		}
		h.mu.Unlock()
		h.wg.Wait()
		if h.controller != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			h.cleanupErr = h.controller.Close(ctx)
			cancel()
		}
	}()
	for {
		var r Request
		if err := ReadMessage(h.conn, &r); err != nil {
			return
		}
		id, err := strconv.ParseUint(r.ID, 10, 64)
		if err != nil || id == 0 || id <= h.lastID || r.Version != ProtocolVersion || r.Build != h.build || len(r.Token) > 0 || r.Media || r.AppControl || r.App == nil {
			return
		}
		h.lastID = id
		if r.Operation != "app_probe" && (r.Resource.Validate() != nil || r.Epoch.NativeEpoch != h.epoch || r.App.Authority.Resource != r.Resource || r.App.Authority.Epoch != r.Epoch) {
			if h.reply(r, nil, appRefusal("stale_authority")) != nil {
				return
			}
			continue
		}
		if r.Operation == "app_cancel" || r.Operation == "app_observer_revoke" {
			h.mu.Lock()
			a := r.App.Authority
			if l := h.leases[r.Resource]; l != nil {
				if a.ObserverGrant != "" && l.observer == a.ObserverGrant {
					if r.Operation == "app_observer_revoke" {
						l.observer = ""
					}
					for id, f := range h.flights {
						if f.authority == a && (r.Operation == "app_observer_revoke" || id == r.App.CancelID) {
							f.cancel()
						}
					}
				} else if r.Operation == "app_cancel" && l.authority == a {
					h.fenceLocked(r.Resource)
				}
			}
			h.mu.Unlock()
			if h.reply(r, nil, nil) != nil {
				return
			}
			continue
		}
		if r.Operation == "app_grant" || r.Operation == "app_renew" || r.Operation == "app_observer_grant" {
			if h.reply(r, nil, h.grant(r)) != nil {
				return
			}
			continue
		}
		if r.Operation == "app_human_grant" {
			if h.reply(r, nil, h.issueHuman(r)) != nil {
				return
			}
			continue
		}
		h.mu.Lock()
		resource, exists := h.resources[r.Resource]
		if r.Operation != "app_probe" && (!exists || resource.epoch != r.Epoch) {
			h.mu.Unlock()
			if h.reply(r, nil, appRefusal("stale_authority")) != nil {
				return
			}
			continue
		}
		if r.Operation == "app_revoke" || r.Operation == "app_quiesce" || r.Operation == "app_dispose" {
			l := h.leases[r.Resource]
			if l == nil || l.authority != r.App.Authority {
				h.mu.Unlock()
				if h.reply(r, nil, appRefusal("stale_authority")) != nil {
					return
				}
				continue
			}
			h.fenceLocked(r.Resource)
			l.observer = ""
			l.human = nil
		}
		if len(h.flights) >= 16 {
			h.mu.Unlock()
			if h.reply(r, nil, appRefusal("app_limit")) != nil {
				return
			}
			continue
		}
		if r.Operation == "app_observe" && r.App.IncludePNG {
			if !validSnapshotID(r.App.SnapshotID) || h.snapshotUsed[r.App.SnapshotID] || len(h.snapshotUsed) >= 4096 {
				h.mu.Unlock()
				if h.reply(r, nil, appRefusal("app_limit")) != nil {
					return
				}
				continue
			}
			h.snapshotUsed[r.App.SnapshotID] = true
		}
		deadline := time.Now().Add(3 * time.Second)
		if l := h.leases[r.Resource]; l != nil && r.Operation == "app_observe" && r.App.Authority.ObserverGrant != "" && l.observerExpiry.Before(deadline) {
			deadline = l.observerExpiry
		}
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		h.flights[r.ID] = appFlight{key: r.Resource, authority: r.App.Authority, cancel: cancel}
		h.wg.Add(1)
		h.mu.Unlock()
		go func() {
			defer h.wg.Done()
			defer cancel()
			out, e := h.execute(ctx, r)
			h.mu.Lock()
			delete(h.flights, r.ID)
			h.mu.Unlock()
			if h.reply(r, out, e) != nil {
				h.conn.Close()
			}
		}()
	}
}

// stop joins the app channel before restoring windows. A failed restoration keeps
// display and process claims alive; FD3 must not destroy their supporting screens.
func (h *appHost) stop() error {
	if h == nil {
		return nil
	}
	h.conn.Close()
	<-h.done
	return h.cleanupErr
}
func (h *appHost) lifecycle(r Request) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	resource, exists := h.resources[r.Resource]
	if !exists || resource.epoch != r.Epoch {
		h.mu.Unlock()
		return appRefusal("stale_authority")
	}
	h.fenceLocked(r.Resource)
	if l := h.leases[r.Resource]; l != nil {
		l.observer = ""
		l.human = nil
	}
	h.mu.Unlock()
	if h.controller == nil {
		return h.controllerErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if r.Operation == "dispose" {
		return h.controller.Dispose(ctx, r.Resource)
	}
	err := h.controller.Quiesce(ctx, r.Resource)
	if err == nil {
		h.mu.Lock()
		if l := h.leases[r.Resource]; l != nil {
			l.quiescent = true
		}
		h.mu.Unlock()
	}
	return err
}
