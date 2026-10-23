package handler

import (
	"context"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) resolveCodeWorkspaceRoot(ctx context.Context, issue db.Issue) (db.Issue, error) {
	return service.ResolveCodeWorkspaceRoot(ctx, h.Queries, issue)
}
