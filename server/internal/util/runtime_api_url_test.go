package util

import "testing"

func TestRuntimeAPIURL(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"https://user:secret@api.example.test:8443/base/?token=private#key", "https://api.example.test:8443/base"},
		{"http://localhost:8080/", "http://localhost:8080"},
		{"https://[::1]:8080/api", "https://[::1]:8080/api"},
		{"https:///missing-host", ""},
		{"file:///private/secret", ""},
		{"not a url", ""},
		{"", ""},
	} {
		t.Run(test.input, func(t *testing.T) {
			if got := RuntimeAPIURL(test.input); got != test.want {
				t.Fatalf("public endpoint = %q, want %q", got, test.want)
			}
		})
	}
}
