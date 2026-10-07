package applicationhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type connectionAccess struct {
	URL              string `json:"url"`
	Token            string `json:"token"`
	CookieName       string `json:"cookie_name"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}

type connectionProxy struct {
	binding   protocol.ApplicationResolvedConnection
	client    *http.Client
	mu        sync.Mutex
	access    connectionAccess
	expires   time.Time
	pathToken string
}

func (p *connectionProxy) resolve(ctx context.Context, force bool) (connectionAccess, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !force && time.Now().Before(p.expires) {
		return p.access, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.binding.ResolverURL, nil)
	if err != nil {
		return connectionAccess{}, err
	}
	request.Header.Set("Authorization", "Bearer "+p.binding.Grant)
	response, err := p.client.Do(request)
	if err != nil {
		return connectionAccess{}, errors.New("application dependency authorization is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return connectionAccess{}, errors.New("application dependency authorization was denied")
	}
	var access connectionAccess
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&access); err != nil {
		return access, errors.New("invalid application dependency authorization")
	}
	address, err := url.Parse(access.URL)
	if err != nil || address.Host == "" || address.User != nil || address.Fragment != "" || address.Scheme != "https" && !(address.Scheme == "http" && (address.Hostname() == "127.0.0.1" || address.Hostname() == "localhost" || strings.HasSuffix(address.Hostname(), ".localhost"))) || access.Token == "" || access.ExpiresInSeconds < 1 || access.ExpiresInSeconds > 28800 || access.CookieName != "multica_app_session" && access.CookieName != "__Host-multica-app" {
		return access, errors.New("invalid application dependency authorization")
	}
	p.access = access
	p.expires = time.Now().Add(time.Duration(access.ExpiresInSeconds/2) * time.Second)
	return access, nil
}

func (p *connectionProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.pathToken != "" {
		prefix := "/" + p.pathToken
		if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
			http.NotFound(w, r)
			return
		}
		r = r.Clone(r.Context())
		path := *r.URL
		path.Path = strings.TrimPrefix(path.Path, prefix)
		if path.Path == "" {
			path.Path = "/"
		}
		path.RawPath = ""
		r.URL = &path
	}
	access, err := p.resolve(r.Context(), false)
	if err != nil {
		http.Error(w, "application dependency is unavailable", http.StatusBadGateway)
		return
	}
	target, err := url.Parse(access.URL)
	if err != nil {
		http.Error(w, "application dependency is unavailable", http.StatusBadGateway)
		return
	}
	proxy := httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(target)
		request.Out.Host = target.Host
		cookies := []string{}
		for _, cookie := range r.Cookies() {
			if cookie.Name != access.CookieName {
				cookies = append(cookies, cookie.String())
			}
		}
		cookies = append(cookies, (&http.Cookie{Name: access.CookieName, Value: access.Token}).String())
		request.Out.Header.Set("Cookie", strings.Join(cookies, "; "))
	}, ModifyResponse: func(response *http.Response) error {
		if response.StatusCode == http.StatusUnauthorized {
			p.mu.Lock()
			p.expires = time.Time{}
			p.mu.Unlock()
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "application dependency is unavailable", http.StatusBadGateway)
	}}
	if r.ProtoMajor == 1 && r.Body != nil && r.Body != http.NoBody {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			http.Error(w, "dependency streaming transport unavailable", 500)
			return
		}
	}
	proxy.ServeHTTP(w, r)
}

func startConnections(ctx context.Context, bindings []protocol.ApplicationResolvedConnection) (map[string]string, func(), error) {
	connectionCtx, cancelConnections := context.WithCancel(ctx)
	values := map[string]string{}
	servers := []*http.Server{}
	done := []chan struct{}{}
	closeAll := func() {
		// Server.Close leaves hijacked streams open; cancel their proxy requests too.
		cancelConnections()
		for _, server := range servers {
			server.Close()
		}
		for _, finished := range done {
			<-finished
		}
	}
	for _, binding := range bindings {
		if binding.LocalURL != "" {
			values[binding.URLVariable] = binding.LocalURL
			continue
		}
		if binding.Grant == "" {
			closeAll()
			return nil, nil, errors.New("remote application dependency has no lifecycle grant")
		}
		resolver, err := url.Parse(binding.ResolverURL)
		if err != nil || resolver.Host == "" || resolver.User != nil || resolver.Path != "/api/application-connections/resolve" || resolver.RawQuery != "" || resolver.Fragment != "" || resolver.Scheme != "https" && resolver.Scheme != "http" {
			closeAll()
			return nil, nil, errors.New("invalid application dependency resolver")
		}
		proxy := &connectionProxy{binding: binding, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			closeAll()
			return nil, nil, err
		}
		proxy.pathToken = hex.EncodeToString(random[:])
		if _, err := proxy.resolve(connectionCtx, false); err != nil {
			closeAll()
			return nil, nil, err
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		server := &http.Server{Handler: proxy, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return connectionCtx }}
		finished := make(chan struct{})
		servers = append(servers, server)
		done = append(done, finished)
		go func() {
			defer close(finished)
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return
			}
		}()
		values[binding.URLVariable] = "http://" + listener.Addr().String() + "/" + proxy.pathToken
	}
	return values, closeAll, nil
}
