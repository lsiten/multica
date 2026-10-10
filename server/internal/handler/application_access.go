package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// applicationOrigin resolves the browser origin for published applications.
// When MULTICA_APPLICATION_ORIGIN is set it is a dedicated per-endpoint
// subdomain origin (dedicated=true). Otherwise applications are served by
// path on the platform public origin (dedicated=false) at /app/{endpoint_id},
// so no MULTICA_APPLICATION_ORIGIN is required to open applications.
func (h *Handler) applicationOrigin() (*url.URL, bool, error) {
	if raw := strings.TrimSpace(h.cfg.ApplicationOrigin); raw != "" {
		origin, err := applicationgateway.Origin(raw)
		if err != nil {
			return nil, false, err
		}
		for _, otherRaw := range []string{h.cfg.PublicURL, h.cfg.AppURL, h.cfg.PluginSurfaceOrigin} {
			other, parseErr := url.Parse(strings.TrimSpace(otherRaw))
			if parseErr == nil && other.Host != "" && strings.EqualFold(other.Host, origin.Host) {
				return nil, false, errors.New("application origin must differ from the platform and plugin origins")
			}
		}
		return origin, true, nil
	}
	if strings.TrimSpace(h.cfg.PublicURL) == "" {
		return nil, false, errors.New("configure MULTICA_PUBLIC_URL or MULTICA_APPLICATION_ORIGIN before opening applications")
	}
	origin, err := url.Parse(strings.TrimSpace(h.cfg.PublicURL))
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, false, errors.New("MULTICA_PUBLIC_URL must be an absolute http/https origin")
	}
	origin.Path = ""
	origin.RawPath = ""
	return origin, false, nil
}

// applicationEndpointURL is the browser URL for one published endpoint. Dedicated
// origins use a per-endpoint subdomain; the public origin serves each endpoint
// under /app/{id} so no MULTICA_APPLICATION_ORIGIN is required.
func applicationEndpointURL(origin *url.URL, dedicated bool, endpointID string) *url.URL {
	address := *origin
	if dedicated {
		address.Host = endpointID + "." + origin.Host
	} else {
		address.Path = "/app/" + endpointID
	}
	return &address
}

// applicationAppBasePath is the URL path prefix for an endpoint on its origin:
// the root for dedicated subdomains, /app/{id} on the public origin.
func applicationAppBasePath(dedicated bool, endpointID string) string {
	if dedicated {
		return ""
	}
	return "/app/" + endpointID
}

func (h *Handler) applicationEndpointForUser(w http.ResponseWriter, r *http.Request) (db.ApplicationEndpoint, db.ApplicationInstance, db.Member, bool) {
	view, workspaceID, _, member, ok := h.loadApplication(w, r)
	if !ok {
		return db.ApplicationEndpoint{}, db.ApplicationInstance{}, db.Member{}, false
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "endpointId"), "endpoint_id")
	if !ok {
		return db.ApplicationEndpoint{}, db.ApplicationInstance{}, member, false
	}
	endpoint, err := h.Queries.GetApplicationEndpoint(r.Context(), db.GetApplicationEndpointParams{ID: id, WorkspaceID: workspaceID})
	if err != nil || uuidToString(endpoint.ApplicationID) != view.ID || endpoint.State != "published" || (endpoint.Visibility == "private" && endpoint.PublishedBy != member.UserID) {
		h.applicationError(w, application.ErrNotFound)
		return endpoint, db.ApplicationInstance{}, member, false
	}
	instance, err := h.Queries.GetApplicationInstance(r.Context(), db.GetApplicationInstanceParams{ID: endpoint.InstanceID, WorkspaceID: workspaceID})
	if err != nil || instance.DesiredState != "running" {
		h.applicationError(w, application.ErrConflict)
		return endpoint, instance, member, false
	}
	if _, ok := h.applicationActor(w, r, workspaceID, member, view.ProjectID); !ok {
		return endpoint, instance, member, false
	}
	return endpoint, instance, member, true
}

// LaunchApplication creates a one-use ticket; platform credentials stay on the management origin.
func (h *Handler) LaunchApplication(w http.ResponseWriter, r *http.Request) {
	endpoint, instance, member, ok := h.applicationEndpointForUser(w, r)
	if !ok {
		return
	}
	origin, dedicated, err := h.applicationOrigin()
	if err != nil {
		writeError(w, http.StatusConflict, "configure MULTICA_APPLICATION_ORIGIN or MULTICA_PUBLIC_URL before opening applications")
		return
	}
	available, err := h.ApplicationGateway.Available(r.Context(), uuidToString(instance.RuntimeID))
	if err != nil {
		h.applicationError(w, err)
		return
	}
	if !available {
		writeError(w, http.StatusServiceUnavailable, "application runtime data channel is offline")
		return
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		h.applicationError(w, err)
		return
	}
	ticket := hex.EncodeToString(random[:])
	if err = h.Queries.CreateApplicationAccessTicket(r.Context(), db.CreateApplicationAccessTicketParams{TokenHash: auth.HashToken(ticket), EndpointID: endpoint.ID, WorkspaceID: endpoint.WorkspaceID, UserID: member.UserID, MemberID: member.ID, EndpointRevision: endpoint.Revision}); err != nil {
		h.applicationError(w, err)
		return
	}
	address := applicationEndpointURL(origin, dedicated, uuidToString(endpoint.ID))
	address.Path = strings.TrimRight(address.Path, "/") + "/.__multica/launch"
	address.RawPath = ""
	address.RawQuery = url.Values{"ticket": {ticket}}.Encode()
	writeJSON(w, http.StatusOK, map[string]string{"url": address.String()})
}

// CreateApplicationServiceAccess issues a revocable, application-scoped service credential.
func (h *Handler) CreateApplicationServiceAccess(w http.ResponseWriter, r *http.Request) {
	endpoint, instance, member, ok := h.applicationEndpointForUser(w, r)
	if !ok {
		return
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{ID: instance.RuntimeID, WorkspaceID: instance.WorkspaceID})
	if err != nil || !canUseRuntimeForAgent(member, runtime) {
		h.applicationError(w, application.ErrForbidden)
		return
	}
	origin, dedicated, err := h.applicationOrigin()
	if err != nil {
		h.applicationError(w, application.ErrConflict)
		return
	}
	token, err := applicationgateway.SignAccess(auth.JWTSecret(), uuidToString(endpoint.ID), uuidToString(endpoint.WorkspaceID), uuidToString(member.UserID), uuidToString(member.ID), endpoint.Revision)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	address := applicationEndpointURL(origin, dedicated, uuidToString(endpoint.ID))
	address.Path = strings.TrimRight(address.Path, "/") + endpoint.EntryPath
	address.RawPath = ""
	writeJSON(w, http.StatusOK, map[string]any{"url": address.String(), "token": "mas_" + token, "expires_in_seconds": 28800})
}

func (h *Handler) applicationSessionCookie(origin *url.URL) string {
	if origin.Scheme == "https" && h.applicationDedicated() {
		return "__Host-multica-app"
	}
	return "multica_app_session"
}

// applicationDedicated reports whether applications use a dedicated subdomain
// origin rather than being served by path on the platform public origin.
func (h *Handler) applicationDedicated() bool {
	return strings.TrimSpace(h.cfg.ApplicationOrigin) != ""
}

func (h *Handler) consumeApplicationLaunch(w http.ResponseWriter, r *http.Request, origin *url.URL, dedicated bool, endpoint db.ApplicationEndpoint) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	value := r.URL.Query().Get("ticket")
	if len(value) != 64 {
		http.Error(w, "application launch ticket is invalid", http.StatusUnauthorized)
		return
	}
	ticket, err := h.Queries.ConsumeApplicationAccessTicket(r.Context(), db.ConsumeApplicationAccessTicketParams{TokenHash: auth.HashToken(value), EndpointID: endpoint.ID})
	if err != nil || ticket.EndpointRevision != endpoint.Revision || ticket.WorkspaceID != endpoint.WorkspaceID {
		http.Error(w, "application launch ticket is expired or already used", http.StatusUnauthorized)
		return
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{UserID: ticket.UserID, WorkspaceID: endpoint.WorkspaceID})
	if err != nil || ticket.MemberID != member.ID || endpoint.Visibility == "private" && endpoint.PublishedBy != member.UserID {
		http.Error(w, "application access is no longer available", http.StatusForbidden)
		return
	}
	token, err := applicationgateway.SignAccess(auth.JWTSecret(), uuidToString(endpoint.ID), uuidToString(endpoint.WorkspaceID), uuidToString(ticket.UserID), uuidToString(member.ID), endpoint.Revision)
	if err != nil {
		http.Error(w, "application launch failed", http.StatusInternalServerError)
		return
	}
	cookiePath := "/"
	if !dedicated {
		cookiePath = "/app"
	}
	http.SetCookie(w, &http.Cookie{Name: h.applicationSessionCookie(origin), Value: token, Path: cookiePath, HttpOnly: true, Secure: origin.Scheme == "https", SameSite: http.SameSiteLaxMode, MaxAge: 28800})
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, strings.TrimRight(applicationAppBasePath(dedicated, uuidToString(endpoint.ID)), "/")+endpoint.EntryPath, http.StatusSeeOther)
}

func (h *Handler) applicationAccessAllowed(r *http.Request, origin *url.URL, endpoint db.ApplicationEndpoint) (applicationgateway.AccessClaims, bool) {
	var token string
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer mas_") {
		token = strings.TrimPrefix(header, "Bearer mas_")
	} else if cookie, err := r.Cookie(h.applicationSessionCookie(origin)); err == nil {
		token = cookie.Value
	}
	claims, err := applicationgateway.ParseAccess(auth.JWTSecret(), token)
	if err != nil || claims.EndpointID != uuidToString(endpoint.ID) || claims.WorkspaceID != uuidToString(endpoint.WorkspaceID) || claims.Revision != endpoint.Revision {
		return claims, false
	}
	userID, err := parseApplicationAccessUser(claims.Subject)
	if err != nil {
		return claims, false
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{UserID: userID, WorkspaceID: endpoint.WorkspaceID})
	if err != nil || uuidToString(member.ID) != claims.MemberID || endpoint.Visibility == "private" && endpoint.PublishedBy != member.UserID {
		return claims, false
	}
	if claims.SourceInstanceID != "" {
		sourceID, err := parseApplicationAccessUser(claims.SourceInstanceID)
		if err != nil {
			return claims, false
		}
		source, err := h.Queries.GetApplicationInstance(r.Context(), db.GetApplicationInstanceParams{ID: sourceID, WorkspaceID: endpoint.WorkspaceID})
		if err != nil || source.DesiredState != "running" || source.Generation != claims.SourceGeneration {
			return claims, false
		}
		target, err := h.Queries.GetApplicationInstance(r.Context(), db.GetApplicationInstanceParams{ID: endpoint.InstanceID, WorkspaceID: endpoint.WorkspaceID})
		if err != nil || target.DesiredState != "running" || target.Generation != claims.TargetGeneration || !h.applicationConnectionRuntimeAllowed(r.Context(), member, source, target) {
			return claims, false
		}
	}
	return claims, true
}

func parseApplicationAccessUser(raw string) (pgtype.UUID, error) {
	var user pgtype.UUID
	err := user.Scan(raw)
	if err != nil || !user.Valid {
		return user, errors.New("invalid application access member")
	}
	return user, nil
}
