package jevmodels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type modelSpec struct {
	Model Model       `json:"model"`
	Files []modelFile `json:"files"`
}

func modelKey(id, revision string) string { return id + "@" + revision }

func (m *Manager) matches(id string, revisions []string) bool {
	return id == m.model.ID && (len(revisions) == 0 || revisions[0] == "" || revisions[0] == m.model.Revision)
}

func (m *Manager) child(id string, revisions []string) (*Manager, error) {
	m.catalogMu.Lock()
	defer m.catalogMu.Unlock()
	if m.ctx.Err() != nil {
		return nil, ErrClosed
	}
	revision := ""
	if len(revisions) > 0 {
		revision = revisions[0]
	}
	key := modelKey(id, revision)
	if revision == "" {
		for candidate, spec := range m.registered {
			if spec.Model.ID == id {
				if key != modelKey(id, "") {
					return nil, errors.New("model revision is required")
				}
				key = candidate
			}
		}
	}
	spec, ok := m.registered[key]
	if !ok {
		return nil, ErrUnknownModel
	}
	if child := m.children[key]; child != nil {
		return child, nil
	}
	hash := sha256.Sum256([]byte(key))
	cfg := m.cfg
	cfg.RootDir = filepath.Join(m.cfg.RootDir, "models", hex.EncodeToString(hash[:]))
	cfg.modelSpec = &spec
	child, err := New(m.ctx, cfg)
	if err != nil {
		return nil, err
	}
	if m.ctx.Err() != nil {
		child.Close()
		return nil, ErrClosed
	}
	m.children[key] = child
	return child, nil
}

func (m *Manager) Catalog() []Model {
	m.catalogMu.Lock()
	defer m.catalogMu.Unlock()
	models := []Model{m.model}
	for _, spec := range m.registered {
		models = append(models, spec.Model)
	}
	sort.Slice(models, func(i, j int) bool {
		return modelKey(models[i].ID, models[i].Revision) < modelKey(models[j].ID, models[j].Revision)
	})
	return models
}

// Register resolves Hub metadata only. Weight download remains an explicit action.
func (m *Manager) Register(ctx context.Context, id, revision string, client *http.Client) (Model, error) {
	spec, err := resolveHubModel(ctx, client, id, revision)
	if err != nil {
		return Model{}, err
	}
	m.catalogMu.Lock()
	defer m.catalogMu.Unlock()
	if m.ctx.Err() != nil {
		return Model{}, ErrClosed
	}
	key := modelKey(spec.Model.ID, spec.Model.Revision)
	if m.matches(spec.Model.ID, []string{spec.Model.Revision}) {
		return m.model, nil
	}
	next := make(map[string]modelSpec, len(m.registered)+1)
	for k, v := range m.registered {
		next[k] = v
	}
	next[key] = spec
	if len(next) > 64 {
		return Model{}, errors.New("local model catalog is full")
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return Model{}, err
	}
	file, err := os.CreateTemp(m.cfg.RootDir, ".catalog-")
	if err != nil {
		return Model{}, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(payload); err != nil {
		file.Close()
		return Model{}, err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return Model{}, err
	}
	if err = file.Close(); err != nil {
		return Model{}, err
	}
	if err = os.Rename(name, filepath.Join(m.cfg.RootDir, "custom-models.json")); err != nil {
		return Model{}, err
	}
	m.registered = next
	return spec.Model, nil
}

func (m *Manager) loadCatalog() error {
	payload, err := os.ReadFile(filepath.Join(m.cfg.RootDir, "custom-models.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(payload) > 1<<20 {
		return errors.New("local model catalog is too large")
	}
	if err = json.Unmarshal(payload, &m.registered); err != nil {
		return fmt.Errorf("read local model catalog: %w", err)
	}
	if len(m.registered) > 64 {
		return errors.New("local model catalog is too large")
	}
	for key, spec := range m.registered {
		if key != modelKey(spec.Model.ID, spec.Model.Revision) || !protocol.ValidLocalJevModel(spec.Model.ID, spec.Model.Revision) || spec.Model.EngineVersion != EngineVersion {
			return errors.New("invalid local model catalog")
		}
		if err := validateModelFiles(spec.Files); err != nil {
			return err
		}
	}
	return nil
}
