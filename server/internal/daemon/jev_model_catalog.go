package daemon

import (
	"encoding/json"
	"io"
	"net/http"
)

func (d *Daemon) jevModelRegisterHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !d.jevLocalAuthorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ModelID  string `json:"model_id"`
			Revision string `json:"revision"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "invalid model registration", http.StatusBadRequest)
			return
		}
		manager, err := d.jevModelManager()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		model, err := manager.Register(r.Context(), req.ModelID, req.Revision, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model)
	}
}
