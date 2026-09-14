package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestResourceKeyValidation(t *testing.T) {
	valid := ResourceKey{BackendIdentity: "https://example.com/api", WorkspaceID: "ws", RuntimeID: "runtime", UID: 501}
	for _, tc := range []struct {
		name   string
		change func(*ResourceKey)
	}{
		{"missing backend", func(k *ResourceKey) { k.BackendIdentity = "" }},
		{"noncanonical backend", func(k *ResourceKey) { k.BackendIdentity += "/" }},
		{"backend credentials", func(k *ResourceKey) { k.BackendIdentity = "https://user:secret@example.com" }},
		{"missing workspace", func(k *ResourceKey) { k.WorkspaceID = "" }},
		{"missing runtime", func(k *ResourceKey) { k.RuntimeID = "" }},
		{"missing login uid", func(k *ResourceKey) { k.UID = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := valid
			tc.change(&k)
			if err := k.Validate(); err == nil {
				t.Fatal("accepted invalid resource identity")
			}
		})
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ResourceKey
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != valid {
		t.Fatalf("resource roundtrip changed identity: %+v", decoded)
	}
}

func TestResourceKeyRejectsNoncanonicalBackendURL(t *testing.T) {
	for _, backend := range []string{
		"https://example.com#",
		"https://example.com/api#",
		"https://example.com:",
		"https://example.com:/api",
		"https://[::1]:",
		"HTTPS://example.com",
		"https://example.com/api path",
	} {
		t.Run(backend, func(t *testing.T) {
			k := ResourceKey{BackendIdentity: backend, WorkspaceID: "ws", RuntimeID: "runtime", UID: 501}

			err := k.Validate()

			if !errors.Is(err, ErrInvalidVscreenContract) {
				t.Fatalf("Validate(%q) = %v, want ErrInvalidVscreenContract", backend, err)
			}
		})
	}
}

func TestResourceKeyAcceptsCanonicalBackendURL(t *testing.T) {
	for _, backend := range []string{
		"https://example.com",
		"http://localhost:8080/api/v1",
		"https://example.com:8443/api",
		"https://[::1]",
		"https://[2001:db8::1]:8443/api",
		"https://[fe80::1%25en0]:8443/api",
		"https://example.com/api%20path",
		"https://example.com/api%23path",
		"https://example.com/api:",
	} {
		t.Run(backend, func(t *testing.T) {
			k := ResourceKey{BackendIdentity: backend, WorkspaceID: "ws", RuntimeID: "runtime", UID: 501}

			err := k.Validate()

			if err != nil {
				t.Fatalf("Validate(%q) = %v, want nil", backend, err)
			}
		})
	}
}

func TestVscreenEpochRejectsMissingGeneration(t *testing.T) {
	for _, epoch := range []VscreenEpoch{{}, {NativeEpoch: "boot"}, {NativeEpoch: "boot", DisplayGeneration: "display"}} {
		if err := epoch.Validate(); err == nil {
			t.Fatalf("accepted incomplete epoch %+v", epoch)
		}
	}
	if err := (VscreenEpoch{NativeEpoch: "boot", DisplayGeneration: "display", GeometryRevision: 1}).Validate(); err != nil {
		t.Fatal(err)
	}
}
