package execenv

import "testing"

func TestRepositoryScopeFingerprintIgnoresPresentationButPreservesBindings(t *testing.T) {
	repos := []RepoContextForEnv{{URL: "https://example.test/a", Ref: "main", Description: "old"}, {URL: "https://example.test/b"}}
	resources := []ProjectResourceForEnv{{ID: "repo", ResourceType: "github_repo", ResourceRef: []byte(`{"url":"https://example.test/a","nested":{"b":2,"a":1}}`), Label: "Old"}}
	first, err := RepositoryScopeFingerprint(repos, resources)
	if err != nil {
		t.Fatal(err)
	}
	reordered := []RepoContextForEnv{repos[1], repos[0]}
	reordered[1].Description = "renamed"
	resources[0].Label = "Renamed"
	resources[0].ResourceRef = []byte(`{ "nested": {"a":1,"b":2}, "url": "https://example.test/a" }`)
	second, err := RepositoryScopeFingerprint(reordered, resources)
	if err != nil || second != first {
		t.Fatalf("display/order change split scope: %s %s %v", first, second, err)
	}
	reordered[1].Ref = "release"
	third, err := RepositoryScopeFingerprint(reordered, resources)
	if err != nil || third == first {
		t.Fatalf("different checkout ref shared scope: %v", err)
	}
	resources[0].ResourceRef = []byte(`{"id":9007199254740992}`)
	large, err := RepositoryScopeFingerprint(nil, resources)
	if err != nil {
		t.Fatal(err)
	}
	resources[0].ResourceRef = []byte(`{"id":9007199254740993}`)
	other, err := RepositoryScopeFingerprint(nil, resources)
	if err != nil || other == large {
		t.Fatalf("large integer resource IDs collapsed: %v", err)
	}
	resources[0].ResourceRef = []byte(`{"id":1} {"id":2}`)
	if _, err := RepositoryScopeFingerprint(nil, resources); err == nil {
		t.Fatal("malformed resource accepted")
	}
}
