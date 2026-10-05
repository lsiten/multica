package handler

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) humanResponseReceipts(ctx context.Context, workspaceID pgtype.UUID, ids []pgtype.UUID) map[string]*service.HumanResponseReceipt {
	result := map[string]*service.HumanResponseReceipt{}
	if len(ids) == 0 {
		return result
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = uuidToString(id)
	}
	requests, err := h.Queries.ListHumanResponseReceipts(ctx, db.ListHumanResponseReceiptsParams{WorkspaceID: workspaceID, ReplyIds: keys})
	if err != nil {
		return result
	}
	for _, request := range requests {
		var response struct {
			Origin service.HumanReplyOrigin `json:"origin"`
		}
		if json.Unmarshal(request.Response, &response) == nil {
			result[response.Origin.ReplyID] = service.ResponseReceipt(request)
		}
	}
	return result
}
