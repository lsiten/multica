package jevmodels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var hubRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sha1Pattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func hubClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		host := req.URL.Hostname()
		trusted := host == "huggingface.co" || strings.HasSuffix(host, ".huggingface.co") || host == "hf.co" || strings.HasSuffix(host, ".hf.co")
		if req.URL.Scheme != "https" || req.URL.User != nil || !trusted || len(via) > 10 {
			return errors.New("unsafe model download redirect")
		}
		return nil
	}}
}

func readHub(ctx context.Context, client *http.Client, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hugging Face HTTP %d; check repository access and revision", res.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, errors.New("Hugging Face metadata is too large")
	}
	return payload, nil
}

func resolveHubModel(ctx context.Context, client *http.Client, id, revision string) (modelSpec, error) {
	empty := modelSpec{}
	if !protocol.ValidLocalJevModel(id, strings.Repeat("0", 40)) {
		return empty, errors.New("expected a Hugging Face model ID: owner/model")
	}
	if revision == "" {
		revision = "main"
	}
	if !hubRef.MatchString(revision) || strings.Contains(revision, "..") {
		return empty, errors.New("invalid Hugging Face revision")
	}
	if client == nil {
		client = hubClient(30 * time.Second)
	}
	raw, err := readHub(ctx, client, "https://huggingface.co/api/models/"+id+"/revision/"+url.PathEscape(revision)+"?blobs=true", 1<<20)
	if err != nil {
		return empty, err
	}
	var metadata struct {
		SHA      string `json:"sha"`
		CardData struct {
			License string `json:"license"`
		} `json:"cardData"`
		Siblings []struct {
			Name   string `json:"rfilename"`
			Size   int64  `json:"size"`
			BlobID string `json:"blobId"`
			LFS    *struct {
				SHA256 string `json:"sha256"`
				Size   int64  `json:"size"`
			} `json:"lfs"`
		} `json:"siblings"`
	}
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return empty, errors.New("invalid Hugging Face metadata")
	}
	if !protocol.ValidLocalJevModel(id, metadata.SHA) {
		return empty, errors.New("Hugging Face did not return a fixed commit")
	}
	files := []modelFile{}
	names := map[string]bool{}
	for _, entry := range metadata.Siblings {
		if path.Base(entry.Name) != entry.Name {
			continue
		}
		extension := path.Ext(entry.Name)
		if extension != ".json" && extension != ".jinja" && extension != ".safetensors" {
			continue
		}
		file := modelFile{Name: entry.Name, Size: entry.Size, GitSHA1: entry.BlobID}
		if entry.LFS != nil {
			file.Size = entry.LFS.Size
			file.SHA256 = entry.LFS.SHA256
			file.GitSHA1 = ""
		}
		if names[file.Name] {
			return empty, errors.New("duplicate model file")
		}
		names[file.Name] = true
		files = append(files, file)
	}
	if !names["config.json"] || !names["decider_config.json"] || !names["tokenizer_config.json"] || !names["tokenizer.json"] || (!names["model.safetensors"] && !names["model.safetensors.index.json"]) {
		return empty, errors.New("model must include Decider configuration, tokenizer and safetensors weights")
	}
	if err = validateModelFiles(files); err != nil {
		return empty, err
	}
	for _, name := range []string{"config.json", "decider_config.json", "model.safetensors.index.json"} {
		if !names[name] {
			continue
		}
		payload, err := readHub(ctx, client, "https://huggingface.co/"+id+"/resolve/"+metadata.SHA+"/"+name, 1<<20)
		if err != nil {
			return empty, err
		}
		var config map[string]json.RawMessage
		if json.Unmarshal(payload, &config) != nil || len(config) == 0 {
			return empty, fmt.Errorf("invalid %s", name)
		}
		if name == "config.json" {
			var modelType string
			if json.Unmarshal(config["model_type"], &modelType) != nil || modelType == "" {
				return empty, errors.New("model architecture is missing")
			}
			if _, remoteCode := config["auto_map"]; remoteCode {
				return empty, errors.New("models requiring repository Python code are not supported")
			}
		}
		if name == "model.safetensors.index.json" {
			var weights map[string]string
			if json.Unmarshal(config["weight_map"], &weights) != nil || len(weights) == 0 {
				return empty, errors.New("invalid safetensors index")
			}
			for _, file := range weights {
				if !names[file] || path.Ext(file) != ".safetensors" {
					return empty, errors.New("safetensors shard is missing")
				}
			}
		}
	}
	var size int64
	for _, file := range files {
		size += file.Size
	}
	return modelSpec{Model: Model{ID: id, Revision: metadata.SHA, EngineVersion: EngineVersion, License: metadata.CardData.License, DownloadBytes: size, Devices: []string{"auto", "cpu", "mps", "cuda"}}, Files: files}, nil
}

func validateModelFiles(files []modelFile) error {
	if len(files) == 0 || len(files) > 512 {
		return errors.New("invalid model manifest")
	}
	var total int64
	for _, file := range files {
		if file.Name == "." || path.Base(file.Name) != file.Name || strings.ContainsAny(file.Name, "\\\x00") || file.Size < 0 || file.Size > 128<<30 {
			return errors.New("invalid model file")
		}
		extension := path.Ext(file.Name)
		if extension != ".json" && extension != ".jinja" && extension != ".safetensors" {
			return errors.New("unsupported model file")
		}
		if !sha256Pattern.MatchString(file.SHA256) && !sha1Pattern.MatchString(file.GitSHA1) {
			return errors.New("model file checksum is missing")
		}
		total += file.Size
		if total > 128<<30 {
			return errors.New("model exceeds the local download limit")
		}
	}
	return nil
}
