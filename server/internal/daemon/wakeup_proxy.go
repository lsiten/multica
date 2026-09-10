package daemon

import (
	"net/http"
	"net/url"
)

func taskWakeupProxy(req *http.Request) (*url.URL, error) {
	proxyURL, err := http.ProxyFromEnvironment(req)
	if err != nil || proxyURL == nil || proxyURL.Scheme != "socks5h" {
		return proxyURL, err
	}
	// Gorilla accepts socks5 but not Go's socks5h alias. Its SOCKS dialer
	// already sends domain names to the proxy, preserving remote DNS. Copy
	// the URL because ProxyFromEnvironment shares its cached configuration.
	proxyCopy := *proxyURL
	proxyCopy.Scheme = "socks5"
	return &proxyCopy, nil
}
