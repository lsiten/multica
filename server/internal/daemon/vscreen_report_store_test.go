package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVscreenReportStoreFailsClosed(t *testing.T) {
	for _, scenario := range []string{"account", "backend", "corrupt", "old_format", "unknown_field", "oversized", "public_file", "symlink", "invalid_sequence"} {
		t.Run(scenario, func(t *testing.T) {
			config, _ := reportTestConfig(t)
			r := reportTestNew(t, config)
			report := reportTestRecord()
			if err := r.Queue(context.Background(), report); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "account":
				config.AccountID = "account-b"
			case "backend":
				config.BackendIdentity = "https://other.example.test"
			case "corrupt":
				os.WriteFile(config.Path, []byte("{"), 0600)
			case "old_format":
				raw, _ := os.ReadFile(config.Path)
				os.WriteFile(config.Path, []byte(strings.Replace(string(raw), `"format":1`, `"format":0`, 1)), 0600)
			case "unknown_field":
				raw, _ := os.ReadFile(config.Path)
				os.WriteFile(config.Path, append([]byte(`{"token":"not-allowed",`), raw[1:]...), 0600)
			case "oversized":
				os.WriteFile(config.Path, make([]byte, vscreenReportFileLimit+1), 0600)
			case "public_file":
				os.Chmod(config.Path, 0644)
			case "symlink":
				target := config.Path + ".target"
				os.Rename(config.Path, target)
				os.Symlink(target, config.Path)
			case "invalid_sequence":
				raw, _ := os.ReadFile(config.Path)
				var disk vscreenReportDisk
				json.Unmarshal(raw, &disk)
				disk.Entries[0].Report.State = "human"
				raw, _ = json.Marshal(disk)
				os.WriteFile(config.Path, raw, 0600)
			}
			if opened, err := newVscreenReporter(config); err == nil {
				opened.Close()
				t.Fatal("unsafe persisted state accepted")
			}
		})
	}
}

func TestVscreenReportStoreCapacityAndWriteFailure(t *testing.T) {
	config, _ := reportTestConfig(t)
	r := reportTestNew(t, config)
	for range vscreenReportLimit {
		if err := r.Queue(context.Background(), reportTestRecord()); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Queue(context.Background(), reportTestRecord()); err != vscreenReportError("report_capacity") {
		t.Fatalf("capacity error=%v", err)
	}
	r.mu.Lock()
	count := len(r.disk.Entries)
	r.mu.Unlock()
	if count != vscreenReportLimit {
		t.Fatalf("entries=%d", count)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	badConfig, _ := reportTestConfig(t)
	bad, err := newVscreenReporter(badConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(badConfig.Path)); err != nil {
		t.Fatal(err)
	}
	if err := bad.Queue(context.Background(), reportTestRecord()); err != vscreenReportError("report_store_failed") {
		t.Fatalf("write failure=%v", err)
	}
	if binding := bad.Bind("generation", func([]byte) (*wsOutbound, error) { t.Error("failed store sent a report"); return nil, nil }); binding != 0 {
		t.Fatal("failed durable store accepted a connection")
	}
	if err := bad.Close(); err != vscreenReportError("report_store_failed") {
		t.Fatalf("close lost store error: %v", err)
	}
	t.Log("128-record bound; failed durable write is explicit and does not report acceptance")
}
