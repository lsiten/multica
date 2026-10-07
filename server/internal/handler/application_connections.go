package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) applicationConnectionRuntimeAllowed(ctx context.Context, member db.Member, instances ...db.ApplicationInstance) bool {
	for _, instance := range instances {
		runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: instance.RuntimeID, WorkspaceID: instance.WorkspaceID})
		if err != nil || !canUseRuntimeForAgent(member, runtime) {
			return false
		}
	}
	return true
}

func (h *Handler) applicationConnectionCommands(commands []protocol.ApplicationControlCommand) error {
	for index := range commands {
		command := &commands[index]
		for bindingIndex := range command.Connections {
			binding := &command.Connections[bindingIndex]
			if binding.LocalURL != "" {
				continue
			}
			grant := applicationgateway.ConnectionGrant{WorkspaceID: command.WorkspaceID, MemberID: command.AccessMemberID, SourceInstanceID: command.InstanceID, SourceGeneration: command.Generation, TargetInstanceID: binding.TargetInstanceID, TargetGeneration: binding.TargetGeneration, RegisteredClaims: jwt.RegisteredClaims{Subject: command.AccessUserID}}
			token, err := applicationgateway.SignConnectionGrant(auth.JWTSecret(), grant)
			if err != nil {
				return err
			}
			binding.Grant = token
			binding.ResolverURL = "/api/application-connections/resolve"
		}
	}
	return nil
}

// ResolveApplicationConnection refreshes a dependency-only session after live generation and membership checks.
func (h *Handler) ResolveApplicationConnection(w http.ResponseWriter, r *http.Request) {
	grant, err := applicationgateway.ParseConnectionGrant(auth.JWTSecret(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid application connection grant")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, grant.WorkspaceID, "workspace_id")
	if !ok {
		return
	}
	sourceID, ok := parseUUIDOrBadRequest(w, grant.SourceInstanceID, "source_instance_id")
	if !ok {
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, grant.TargetInstanceID, "target_instance_id")
	if !ok {
		return
	}
	userID, ok := parseUUIDOrBadRequest(w, grant.Subject, "user_id")
	if !ok {
		return
	}
	source, err := h.Queries.GetApplicationInstance(r.Context(), db.GetApplicationInstanceParams{ID: sourceID, WorkspaceID: workspaceID})
	if err != nil || source.DesiredState != "running" || source.Generation != grant.SourceGeneration {
		writeError(w, http.StatusForbidden, "source application connection was revoked")
		return
	}
	target, err := h.Queries.GetApplicationInstance(r.Context(), db.GetApplicationInstanceParams{ID: targetID, WorkspaceID: workspaceID})
	if err != nil || target.DesiredState != "running" || target.Generation != grant.TargetGeneration {
		writeError(w, http.StatusForbidden, "target application connection was revoked")
		return
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{UserID: userID, WorkspaceID: workspaceID})
	if err != nil || uuidToString(member.ID) != grant.MemberID {
		writeError(w, http.StatusForbidden, "application connection membership was revoked")
		return
	}
	if !h.applicationConnectionRuntimeAllowed(r.Context(), member, source, target) {
		writeError(w, http.StatusForbidden, "application connection runtime access was revoked")
		return
	}
	endpoints, err := h.Queries.ListApplicationEndpoints(r.Context(), db.ListApplicationEndpointsParams{WorkspaceID: workspaceID, ApplicationID: target.ApplicationID})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	for _, endpoint := range endpoints {
		if endpoint.InstanceID != target.ID || endpoint.State != "published" || endpoint.Visibility == "private" && endpoint.PublishedBy != userID {
			continue
		}
		origin, err := h.applicationOrigin()
		if err != nil {
			writeError(w, http.StatusConflict, "configure application origin before connecting remote services")
			return
		}
		token, err := applicationgateway.SignConnectionAccess(auth.JWTSecret(), uuidToString(endpoint.ID), endpoint.Revision, grant)
		if err != nil {
			h.applicationError(w, err)
			return
		}
		address := applicationgateway.EndpointOrigin(origin, uuidToString(endpoint.ID))
		address.Path = endpoint.EntryPath
		writeJSON(w, http.StatusOK, map[string]any{"url": address.String(), "token": token, "cookie_name": h.applicationSessionCookie(origin), "expires_in_seconds": 28800})
		return
	}
	writeError(w, http.StatusConflict, "publish the selected dependency before connecting remote services")
}
