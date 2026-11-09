package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"
)

const deviceDialTimeout = 5 * time.Second

// errFallback marks a box reply of 404 that the local catalog answers.
var errFallback = errors.New("box has no such route")

// proxyOrLocal sends every Tiiny name to the box at the address
// discovery selected (USB first, then Wi-Fi); device() returns "" when
// no box answers and is read on every request, so a box that appears
// or changes link after start is followed without a restart.
//
// The local catalog fills two gaps: a 404 from the box on a path the
// catalog implements (routes this firmware does not serve), and, for
// the catalog names, no box at all. Sign-in, unlock and model names
// (deviceName) are never answered locally: the box's own state, such
// as a locked data volume after power-up, has to reach the app.
// Requests without a Tiiny name (the PC service routes) stay local.
func proxyOrLocal(local *http.ServeMux, device func() string) http.Handler {
	proxy := deviceProxy(local)
	cache := &boxCache{
		entries:   map[string]*boxAnswer{},
		transport: &http.Transport{DialContext: (&net.Dialer{Timeout: deviceDialTimeout}).DialContext},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		if !deviceName(host) && !catalogName(host) {
			local.ServeHTTP(w, r)
			return
		}
		addr := device()
		if addr == "" {
			if catalogName(host) {
				local.ServeHTTP(w, r)
				return
			}
			http.Error(w, "no device", http.StatusBadGateway)
			return
		}
		// A fallback replays the body, so it is kept (bounded by maxBody).
		if r.Body != nil && r.Body != http.NoBody {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
			r.Body.Close()
			if err != nil {
				http.Error(w, "bad request body", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		}
		// The box sees the name alone, as it would from a direct request.
		r.Host = host
		if slowBoxRoutes[r.Method+" "+host+r.URL.Path] {
			r.Body = bodyReplay(r)
			cache.serve(w, r, addr)
			return
		}
		r.URL.Scheme = "http"
		r.URL.Host = addr
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			w = &wsLogWriter{ResponseWriter: w, label: host + r.URL.Path}
		}
		proxy.ServeHTTP(w, r)
	})
}

// wsLogWriter logs how long a proxied websocket lasted and which side
// ended it, so a socket the app keeps reopening shows up in catalog.log.
type wsLogWriter struct {
	http.ResponseWriter
	label string
}

func (w *wsLogWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	return &wsLogConn{Conn: conn, label: w.label, start: time.Now()}, rw, nil
}

type wsLogConn struct {
	net.Conn
	label string
	start time.Time
	once  sync.Once
	mu    sync.Mutex
	ended string
}

func (c *wsLogConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		c.mu.Lock()
		if c.ended == "" {
			c.ended = "app closed: " + err.Error()
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *wsLogConn) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		why := c.ended
		c.mu.Unlock()
		if why == "" {
			why = "box side ended"
		}
		log.Printf("ws %s ended after %s (%s)", c.label, time.Since(c.start).Round(time.Second), why)
	})
	return c.Conn.Close()
}

// boxAlias names the box route that serves a request the box answered
// with 404, where app and box software disagree on the route:
//   - box OS 0.1.33 has no /api/services/apps/metadata (the agent store
//     list) but serves the same {app, categories, carousel} at
//     api.tiiny/api/v1/apps;
//   - box OS 0.1.39 serves /api/v1/sys/wifi/* only on wifi.api.tiiny,
//     while app 1.0.3 also asks api.tiiny.
func boxAlias(host, path string) (string, string, bool) {
	switch {
	case path == "/api/services/apps/metadata":
		return "api.tiiny", "/api/v1/apps", true
	case host == "api.tiiny" && strings.HasPrefix(path, "/api/v1/sys/wifi/"):
		return "wifi.api.tiiny", path, true
	}
	return "", "", false
}

// errAlias marks a box 404 on a route boxAlias covers.
var errAlias = errors.New("box serves this under another path")

// deviceProxy forwards a request whose URL already names the box.
// FlushInterval -1 writes every chunk at once, so streamed chat
// completions reach the UI token by token. Websocket upgrades are
// passed through by ReverseProxy. Every non-2xx box reply and every
// local answer is logged with method, host and path.
func deviceProxy(local *http.ServeMux) *httputil.ReverseProxy {
	var proxy *httputil.ReverseProxy
	proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = pr.In.URL.Scheme
			pr.Out.URL.Host = pr.In.URL.Host
			pr.Out.Host = pr.In.Host
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: deviceDialTimeout}).DialContext,
		},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			req := resp.Request
			if resp.StatusCode < 400 {
				return nil
			}
			if _, _, ok := boxAlias(req.Host, req.URL.Path); ok && resp.StatusCode == http.StatusNotFound {
				// Expected on this box OS; the alias target logs its own failures.
				resp.Body.Close()
				return errAlias
			}
			log.Printf("box %s %s%s: %d", req.Method, req.Host, req.URL.Path, resp.StatusCode)
			if resp.StatusCode != http.StatusNotFound {
				return nil
			}
			if _, pattern := local.Handler(req); pattern == "" {
				return nil
			}
			resp.Body.Close()
			return errFallback
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// r is the outgoing request; its body was sent to the box.
			r.Body = http.NoBody
			if r.GetBody != nil {
				r.Body, _ = r.GetBody()
			}
			switch {
			case errors.Is(err, errAlias):
				host, path, _ := boxAlias(r.Host, r.URL.Path)
				again := r.Clone(r.Context())
				again.Host = host
				again.URL.Path, again.URL.RawPath = path, ""
				proxy.ServeHTTP(w, again)
			case errors.Is(err, errFallback):
				log.Printf("box %s%s: answered by the local catalog", r.Host, r.URL.Path)
				local.ServeHTTP(w, r)
			default:
				log.Printf("box %s %s%s: %v", r.Method, r.Host, r.URL.Path, err)
				http.Error(w, "device unavailable", http.StatusBadGateway)
			}
		},
	}
	return proxy
}

// deviceName reports the names only the box may answer.
func deviceName(host string) bool {
	switch host {
	case "api.tiiny", "openai.api.tiiny", "anthropic.api.tiiny", "ollama.api.tiiny", "kb.api.tiiny", "tts.api.tiiny", "connector.api.tiiny", "backup.managed.tiiny", "auth.api.tiiny", "hardware-upgrade.api.tiiny":
		return true
	default:
		return false
	}
}

// catalogName reports the box names the local catalog stands in for.
func catalogName(host string) bool {
	switch host {
	case "p8800.api.tiiny", "agent-services.api.tiiny", "chat-history.api.tiiny", "wifi.api.tiiny":
		return true
	default:
		return false
	}
}
