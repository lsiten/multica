package handler

import (
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type documentContentChangedPayload struct {
	OwnerType string `json:"owner_type"`
	OwnerID   string `json:"owner_id"`
	Revision  int64  `json:"revision"`
}

func (h *Handler) publishDocumentContentVersion(version db.DocumentContentVersion) {
	h.publish(protocol.EventDocumentContentChanged, uuidToString(version.WorkspaceID), version.ActorType, uuidToString(version.ActorID), documentContentChangedPayload{
		OwnerType: version.OwnerType,
		OwnerID:   uuidToString(version.OwnerID),
		Revision:  version.Revision,
	})
}
