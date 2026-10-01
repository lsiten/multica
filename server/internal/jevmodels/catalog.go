package jevmodels

import "errors"

const ModelID = "Mapika/decider-2b"
const Revision = "533964dae8be954c5b5e19fa4948e48408094c1e"
const EngineVersion = "1.8.1"

var (
	ErrUnknownModel = errors.New("model is not in the curated catalog")
	ErrBusy         = errors.New("model has active work")
	ErrNotInstalled = errors.New("model is not installed; explicitly download it first")
	ErrClosed       = errors.New("model manager is closed")
)

type Model struct {
	ID            string   `json:"id"`
	Revision      string   `json:"revision"`
	EngineVersion string   `json:"engine_version"`
	License       string   `json:"license"`
	DownloadBytes int64    `json:"download_bytes"`
	Devices       []string `json:"devices"`
}

type modelFile struct {
	Name   string
	Size   int64
	SHA256 string
}

// Hashes are from the fixed Hub revision, not from user-supplied repositories.
var modelFiles = []modelFile{
	{"config.json", 1790, "6cb8daca9fb653c61485ff7452fc068bacd5c27cbee659ecd24b47186b0d1b52"},
	{"decider_config.json", 1240, "6e4891f2754a1c18a10f8dadb0c04e439e7f79fab0333d56641491bd4a05e722"},
	{"tokenizer_config.json", 1127, "171ecbe7ddae98d11840698f7df2b8d5b4722139db0f0620d3bbf429bd656250"},
	{"chat_template.jinja", 7755, "273d8e0e683b885071fb17e08d71e5f2a5ddfb5309756181681de4f5a1822d80"},
	{"generation_config.json", 116, "62153eb6c69f2e1f426beaa8002b7186437e949c7588167085df14e10e9c0a73"},
	{"tokenizer.json", 19989325, "06b9509352d2af50381ab2247e083b80d32d5c0aba91c272ca9ff729b6a0e523"},
	{"model.safetensors", 3763692048, "acaef2228b134dcdc20cad4ee79219482c927ec819aa3687b9b8a575c338817f"},
}

func Catalog() []Model {
	var size int64
	for _, f := range modelFiles {
		size += f.Size
	}
	return []Model{{ID: ModelID, Revision: Revision, EngineVersion: EngineVersion, License: "apache-2.0", DownloadBytes: size, Devices: []string{"auto", "cpu", "mps", "cuda"}}}
}

type Selection struct {
	ModelID string
	Device  string
}

func (s Selection) validate() error {
	if s.ModelID != ModelID {
		return ErrUnknownModel
	}
	switch s.Device {
	case "auto", "cpu", "mps", "cuda":
		return nil
	default:
		return errors.New("unsupported model device")
	}
}
