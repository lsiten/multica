package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuiltinMCPDetailsRequireHostProfileAndExposeOnlyPublicContracts(t *testing.T) {
	d := &Daemon{cfg: Config{Profile: "fixture"}, client: NewClient("https://example.test")}
	d.client.SetToken("private-token")
	for _, authorized := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/mcp/services", nil)
		if authorized {
			request.Header.Set("Authorization", "Bearer private-token")
			request.Header.Set("X-Multica-Profile", "fixture")
		}
		response := httptest.NewRecorder()
		d.builtinMCPDetailsHandler()(response, request)
		if !authorized {
			if response.Code != 401 {
				t.Fatal(response.Code)
			}
			continue
		}
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "private-token") || strings.Contains(response.Body.String(), "http://127.") {
			t.Fatal("private transport data exposed")
		}
		var body struct {
			Services  []builtinMCPDetails    `json:"services"`
			Instances []MCPReadinessSnapshot `json:"instances"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Services) != 2 || len(body.Services[0].Tools) != 4 || !body.Services[1].RequiresCapability {
			t.Fatal(body)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp/services", nil)
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("X-Multica-Profile", "fixture")
	request.Header.Set("Origin", "https://foreign.example")
	response := httptest.NewRecorder()
	d.builtinMCPDetailsHandler()(response, request)
	if response.Code != 401 {
		t.Fatal("cross-origin request accepted")
	}
}
