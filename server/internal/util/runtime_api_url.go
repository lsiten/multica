package util

import (
	"net/url"
	"strings"
)

// RuntimeAPIURL returns the public HTTP endpoint without authentication data.
func RuntimeAPIURL(raw string) string {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return ""
	}
	endpoint.User = nil
	endpoint.RawQuery = ""
	endpoint.ForceQuery = false
	endpoint.Fragment = ""
	return strings.TrimRight(endpoint.String(), "/")
}
