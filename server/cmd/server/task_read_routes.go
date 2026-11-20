package main

import (
	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/handler"
)

func registerTaskReadRoutes(r chi.Router, h *handler.Handler) {
	r.Get("/api/tasks/{taskId}/messages", h.ListTaskMessagesByUser)
	r.Get("/api/tasks/{taskId}/answers", h.ListAgentAnswers)
	r.Post("/api/tasks/{taskId}/collaboration/append", h.AppendAgent)
	r.Post("/api/tasks/{taskId}/collaboration/next", h.NextAgentTurn)
	r.Post("/api/tasks/{taskId}/collaboration/watch", h.WatchAgentRun)
	r.Post("/api/tasks/{taskId}/collaboration/stop", h.StopAgentRun)
	r.Post("/api/issues/{id}/collaboration/delegate", h.DelegateAgent)
	r.Get("/api/agent-collaboration/operations/{operationId}", h.GetAgentCollaborationOperation)
	r.Get("/api/tasks/{taskId}/context-receipt", h.GetTaskContextReceipt)
	r.With(handler.RequireHumanActor).Post("/api/tasks/{taskId}/retry-source-context", h.RetrySourceContextQuickCreate)
}
