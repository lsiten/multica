package llm2jev

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSystemOneValidationIdentifiesMalformedFields(t *testing.T) {
	cases := []struct{ raw, field string }{
		{`{"state":false,"questions":{"route":{"type":"noul","instructions":"ready?"}}}`, "state"},
		{`{"state":"evidence","questions":{}}`, "questions"},
		{`{"state":"evidence","questions":{"route":{"type":"choice","instructions":"choose","criteria":["a","b"]}}}`, "questions.*.criteria"},
		{`{"state":"evidence","questions":{"route":{"type":"score","instructions":"quality","criteria":{"low":"a","high":"b"}}}}`, "questions.*.criteria"},
		{`{"state":"evidence","questions":{"route":{"type":"noul"}}}`, "questions.*.instructions"},
		{`{"state":"evidence","questions":{"route":{"type":"noul","criteria":{"yes":"ready","no":"not ready"}}}}`, "questions.*.criteria"},
		{`{"state":"evidence","questions":{"route":{"type":"boolean","instructions":"ready?"}}}`, "questions.*.type"},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			var input SystemOneRequest
			if err := json.Unmarshal([]byte(c.raw), &input); err != nil {
				t.Fatal(err)
			}
			err := input.Validate()
			var fieldError *InputError
			if !errors.Is(err, ErrInvalidRequest) || !errors.As(err, &fieldError) || fieldError.Field != c.field {
				t.Fatalf("expected actionable error for %s, got %v", c.field, err)
			}
		})
	}
}
