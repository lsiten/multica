package service

import (
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/multica-ai/multica/server/internal/storage"
)

var (
	uploadReferenceID  = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	uploadReferenceURL = regexp.MustCompile(`(?:https?://|/uploads/)[^\s"\\<>\[\]{}()]+`)
)

type uploadReferences struct {
	ids  map[string]bool
	keys map[string]bool
}

func newUploadReferences() uploadReferences {
	return uploadReferences{ids: make(map[string]bool), keys: make(map[string]bool)}
}

func (r uploadReferences) add(store storage.Storage, document string) {
	document = html.UnescapeString(strings.ReplaceAll(document, `\/`, `/`))
	for _, id := range uploadReferenceID.FindAllString(document, -1) {
		r.ids[strings.ToLower(id)] = true
	}
	for _, raw := range uploadReferenceURL.FindAllString(document, -1) {
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		parsed.RawQuery, parsed.Fragment = "", ""
		key := store.KeyFromURL(parsed.String())
		r.keys[key] = true
		if decoded, err := url.PathUnescape(key); err == nil {
			r.keys[decoded] = true
		}
	}
}
