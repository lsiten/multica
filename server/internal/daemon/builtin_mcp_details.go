package daemon

import (
	"encoding/json"
	"net/http"
)

type builtinMCPDetails struct {
	Name               string           `json:"name"`
	Transport          string           `json:"transport"`
	RequiresCapability bool             `json:"requires_capability"`
	Tools              []map[string]any `json:"tools"`
}

// Expose public tool contracts, never task routes, credentials or bearer tokens.
func (d *Daemon) builtinMCPDetailsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !d.jevLocalAuthorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		services := []builtinMCPDetails{
			{Name: llm2jevMCPName, Transport: "streamable_http", Tools: []map[string]any{llm2jevCapabilitiesDescriptor(), llm2jevToolDescriptor(), jevSystemOneDescriptor(), llm2jevCompletionDescriptor()}},
			{Name: identityActionsMCPName, Transport: "streamable_http", RequiresCapability: true, Tools: []map[string]any{identitySendEmailToolDescriptor()}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"services": services, "instances": d.mcpReadinessSnapshot()})
	}
}
