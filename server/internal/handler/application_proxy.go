package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ApplicationHostBoundary gives endpoint hosts application-only routing, before platform auth.
func (h *Handler) ApplicationHostBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, dedicated, err := h.applicationOrigin()
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		base := strings.ToLower(origin.Host)
		var (
			endpointID pgtype.UUID
			restPath   string
		)
		if dedicated {
			host := strings.ToLower(r.Host)
			if host == base {
				http.NotFound(w, r)
				return
			}
			if !strings.HasSuffix(host, "."+base) {
				next.ServeHTTP(w, r)
				return
			}
			parsed, ok := parseUUIDOrBadRequest(w, strings.TrimSuffix(host, "."+base), "endpoint_id")
			if !ok {
				return
			}
			endpointID = parsed
			restPath = r.URL.Path
		} else {
			host := strings.ToLower(r.Host)
			if host != base || !strings.HasPrefix(r.URL.Path, "/app/") {
				next.ServeHTTP(w, r)
				return
			}
			segments := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/app/"), "/", 2)
			parsed, ok := parseUUIDOrBadRequest(w, segments[0], "endpoint_id")
			if !ok {
				return
			}
			endpointID = parsed
			if len(segments) == 2 {
				restPath = "/" + segments[1]
			}
		}
		endpoint, err := h.Queries.GetPublishedApplicationEndpoint(r.Context(), endpointID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		if restPath == "/.__multica/launch" {
			h.consumeApplicationLaunch(w, r, origin, dedicated, endpoint)
			return
		}
		if strings.HasPrefix(restPath, "/.__multica/") {
			http.NotFound(w, r)
			return
		}
		claims, allowed := h.applicationAccessAllowed(r, origin, endpoint)
		if !allowed {
			http.Error(w, "open this application from Multica to sign in", http.StatusUnauthorized)
			return
		}
		instance, err := h.Queries.GetApplicationInstance(r.Context(), db.GetApplicationInstanceParams{ID: endpoint.InstanceID, WorkspaceID: endpoint.WorkspaceID})
		if err != nil || instance.DesiredState != "running" || instance.ProcessState != "running" || instance.Generation != instance.ObservedGeneration || instance.Revision != instance.ObservedRevision {
			http.Error(w, "application service is not running", http.StatusServiceUnavailable)
			return
		}
		proxyCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-proxyCtx.Done():
					return
				case <-ticker.C:
					current, err := h.Queries.GetPublishedApplicationEndpoint(proxyCtx, endpoint.ID)
					if err != nil || current.Revision != endpoint.Revision || claims.ExpiresAt == nil || time.Now().After(claims.ExpiresAt.Time) {
						h.ApplicationGateway.Revoke(uuidToString(endpoint.ID))
						cancel()
						return
					}
					if _, allowed := h.applicationAccessAllowed(r.WithContext(proxyCtx), origin, current); !allowed {
						cancel()
						return
					}
					observed, err := h.Queries.GetApplicationInstance(proxyCtx, db.GetApplicationInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID})
					if err != nil || observed.DesiredState != "running" || observed.ProcessState != "running" || observed.Generation != instance.Generation || observed.Generation != observed.ObservedGeneration {
						cancel()
						return
					}
				}
			}
		}()
		defer func() { cancel(); <-watchDone }()
		request := protocol.ApplicationTunnelRequest{EndpointID: uuidToString(endpoint.ID), InstanceID: uuidToString(instance.ID), WorkspaceID: uuidToString(instance.WorkspaceID), RuntimeID: uuidToString(instance.RuntimeID), Generation: instance.Generation, Kind: "service", Port: int(endpoint.Port)}
		transport := &http.Transport{DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return h.ApplicationGateway.Open(ctx, request)
		}}
		defer transport.CloseIdleConnections()
		proxy := httputil.ReverseProxy{Transport: transport, FlushInterval: -1, Rewrite: func(proxy *httputil.ProxyRequest) {
			proxy.Out.URL.Scheme = "http"
			proxy.Out.URL.Host = "127.0.0.1:" + strconv.Itoa(int(endpoint.Port))
			proxy.Out.URL.Path = restPath
			proxy.Out.URL.RawPath = ""
			proxy.Out.Host = proxy.Out.URL.Host
			proxy.Out.Header.Set("X-Forwarded-Host", r.Host)
			proxy.Out.Header.Set("X-Forwarded-Proto", origin.Scheme)
			if applicationManagementAuthorization(proxy.Out.Header.Get("Authorization")) {
				proxy.Out.Header.Del("Authorization")
			}
			cookies := []string{}
			for _, cookie := range r.Cookies() {
				if cookie.Name != h.applicationSessionCookie(origin) && !strings.HasPrefix(cookie.Name, "multica") && !strings.HasPrefix(cookie.Name, "__Host-multica") {
					cookies = append(cookies, cookie.String())
				}
			}
			proxy.Out.Header.Del("Cookie")
			if len(cookies) > 0 {
				proxy.Out.Header.Set("Cookie", strings.Join(cookies, "; "))
			}
			for name := range proxy.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-multica-") || name == "X-Workspace-Id" || name == "X-Workspace-Slug" || name == "X-Actor-Source" || name == "X-Agent-Id" || name == "X-Task-Id" || name == "X-User-Id" {
					proxy.Out.Header.Del(name)
				}
			}
		}, ModifyResponse: func(response *http.Response) error {
			cookies := response.Cookies()
			response.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if cookie.Name == h.applicationSessionCookie(origin) || strings.HasPrefix(cookie.Name, "__Host-multica") || strings.HasPrefix(cookie.Name, "multica_") {
					continue
				}
				cookie.Domain = ""
				response.Header.Add("Set-Cookie", cookie.String())
			}
			return nil
		}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "application runtime is unavailable", http.StatusBadGateway)
		}}
		if r.ProtoMajor == 1 && r.Body != nil && r.Body != http.NoBody {
			if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
				http.Error(w, "application streaming transport is unavailable", http.StatusInternalServerError)
				return
			}
		}
		proxy.ServeHTTP(w, r.WithContext(proxyCtx))
	})
}

func applicationManagementAuthorization(header string) bool {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	for _, prefix := range []string{"mas_", "mul_", "mat_", "mdt_", "mcn_"} {
		if strings.HasPrefix(parts[1], prefix) {
			return true
		}
	}
	// Expired platform credentials must stay isolated too; signature ownership
	// determines whether forwarding is safe, not whether authentication succeeds.
	token, err := jwt.Parse(parts[1], func(token *jwt.Token) (any, error) {
		return auth.JWTSecret(), nil
	}, jwt.WithValidMethods([]string{"HS256", "HS384", "HS512"}), jwt.WithoutClaimsValidation())
	return err == nil && token.Valid
}

// GetApplicationLogs reads bounded local output only for members who can use the target runtime.
func (h *Handler) GetApplicationLogs(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, appID, member, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	instanceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "instanceId"), "instance_id")
	if !ok {
		return
	}
	instance, err := h.Queries.GetApplicationInstance(r.Context(), db.GetApplicationInstanceParams{ID: instanceID, WorkspaceID: workspaceID})
	if err != nil || instance.ApplicationID != appID {
		h.applicationError(w, application.ErrNotFound)
		return
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{ID: instance.RuntimeID, WorkspaceID: workspaceID})
	if err != nil || !canUseRuntimeForAgent(member, runtime) {
		h.applicationError(w, application.ErrForbidden)
		return
	}
	limit := 65536
	if len(r.URL.Query().Get("cursor")) > 512 {
		writeError(w, http.StatusBadRequest, "invalid application log cursor")
		return
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 65536 {
			writeError(w, http.StatusBadRequest, "invalid application log limit")
			return
		}
	}
	stream, err := h.ApplicationGateway.Open(r.Context(), protocol.ApplicationTunnelRequest{InstanceID: uuidToString(instance.ID), WorkspaceID: uuidToString(workspaceID), RuntimeID: uuidToString(instance.RuntimeID), Generation: instance.Generation, Kind: "logs", Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		h.applicationError(w, errors.New("application log data channel is unavailable"))
		return
	}
	defer stream.Close()
	var page applicationhost.LogPage
	if err = json.NewDecoder(io.LimitReader(stream, 128<<10)).Decode(&page); err != nil {
		h.applicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
