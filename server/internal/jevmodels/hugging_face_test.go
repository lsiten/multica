package jevmodels

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureHub(t *testing.T, revision string, contents map[string]string, calls *[]string) *http.Client {
	t.Helper()
	siblings := []map[string]any{}
	for name, body := range contents {
		hash := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(body), body)))
		siblings = append(siblings, map[string]any{"rfilename": name, "size": len(body), "blobId": hex.EncodeToString(hash[:])})
	}
	metadata, _ := json.Marshal(map[string]any{"sha": revision, "siblings": siblings, "cardData": map[string]any{"license": "apache-2.0"}})
	return &http.Client{Transport: fakeTransport(func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, request.URL.String())
		body := ""
		if strings.HasPrefix(request.URL.Path, "/api/models/") {
			body = string(metadata)
			if request.URL.Query().Get("blobs") != "true" {
				t.Error("file metadata missing")
			}
		} else {
			if !strings.Contains(request.URL.Path, "/resolve/"+revision+"/") {
				t.Error("mutable revision used", request.URL)
			}
			body = contents[filepath.Base(request.URL.Path)]
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
}
func hubFiles() map[string]string {
	return map[string]string{"config.json": `{"model_type":"qwen3_5"}`, "decider_config.json": `{"temperature":1.1}`, "tokenizer.json": `{"version":"1"}`, "tokenizer_config.json": `{"model_type":"qwen3_5"}`, "model.safetensors": "fixture-weights"}
}

func TestRegisterPinsMetadataWithoutDownloadingAndSurvivesRestart(t *testing.T) {
	m := newTestManager(t)
	revision := strings.Repeat("a", 40)
	calls := []string{}
	model, err := m.Register(t.Context(), "Example/decider", "main", fixtureHub(t, revision, hubFiles(), &calls))
	if err != nil {
		t.Fatal(err)
	}
	if model.Revision != revision || len(m.Catalog()) != 2 {
		t.Fatal(model, m.Catalog())
	}
	for _, address := range calls {
		if strings.HasSuffix(address, "model.safetensors") {
			t.Fatal("registration downloaded weights")
		}
	}
	status, err := m.Status(model.ID, revision)
	if err != nil || status.State != "not_installed" {
		t.Fatal(status, err)
	}
	if _, err = m.Acquire(t.Context(), Selection{ModelID: model.ID, Revision: revision, Device: "cpu"}); !errors.Is(err, ErrNotInstalled) {
		t.Fatal(err)
	}
	if _, err = m.Acquire(t.Context(), Selection{ModelID: model.ID, Revision: strings.Repeat("b", 40), Device: "cpu"}); !errors.Is(err, ErrUnknownModel) {
		t.Fatal("wrong revision accepted", err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(t.Context(), m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.Catalog()) != 2 {
		t.Fatal("custom catalog was not restored")
	}
}

func TestHubRejectsIncompatibleModelsAndUnsafeRepositoryIDs(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter func(map[string]string)
	}{
		{"missing decision config", func(files map[string]string) { delete(files, "decider_config.json") }},
		{"remote code", func(files map[string]string) {
			files["config.json"] = `{"model_type":"custom","auto_map":{"AutoConfig":"remote.Config"}}`
		}},
		{"missing shard", func(files map[string]string) {
			delete(files, "model.safetensors")
			files["model.safetensors.index.json"] = `{"weight_map":{"weights":"missing.safetensors"}}`
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := hubFiles()
			test.alter(files)
			calls := []string{}
			_, err := resolveHubModel(t.Context(), fixtureHub(t, strings.Repeat("a", 40), files, &calls), "Example/model", "main")
			if err == nil {
				t.Fatal("incompatible model accepted")
			}
		})
	}
	for _, id := range []string{"../model", "http://localhost/model", "owner/model/other", "owner/../model"} {
		_, err := resolveHubModel(t.Context(), nil, id, "main")
		if err == nil {
			t.Fatal("unsafe repository accepted", id)
		}
	}
}

func TestCustomFilesVerifyGitBlobChecksumAndRevision(t *testing.T) {
	calls := []string{}
	revision := strings.Repeat("a", 40)
	files := hubFiles()
	client := fixtureHub(t, revision, files, &calls)
	spec, err := resolveHubModel(t.Context(), client, "Example/model", "release")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, file := range spec.Files {
		if err = downloadFile(t.Context(), client, dir, spec.Model, file, func(int64) {}); err != nil {
			t.Fatal(err)
		}
	}
	if err = verifyFiles(t.Context(), dir, spec.Files); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "model.safetensors"), []byte("wrong-weights!!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = verifyFiles(t.Context(), dir, spec.Files); err == nil {
		t.Fatal("corrupt custom weights accepted")
	}
}

func TestCustomModelLeasesShareOwnershipAndPreventRemoval(t *testing.T) {
	m := newTestManager(t)
	revision := strings.Repeat("a", 40)
	calls := []string{}
	model, err := m.Register(t.Context(), "Example/model", "main", fixtureHub(t, revision, hubFiles(), &calls))
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.child(model.ID, []string{revision})
	if err != nil {
		t.Fatal(err)
	}
	fakeReady(child)
	lease, err := m.Acquire(t.Context(), Selection{ModelID: model.ID, Revision: revision, Device: "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Remove(model.ID, revision); !errors.Is(err, ErrBusy) {
		t.Fatal("active custom model removed", err)
	}
	lease.Release()
	if status, err := m.Status(model.ID, revision); err != nil || status.ActiveLeases != 0 {
		t.Fatal(status, err)
	}
}
