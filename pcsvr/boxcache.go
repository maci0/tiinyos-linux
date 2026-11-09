package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"maps"
	"net/http"
	"sync"
	"time"
)

const (
	// Box OS 0.1.33 takes 7 to 14 s to answer POST /api/upgrade/check
	// (a 1.2 MB document); the app gives up after 5 s and polls again.
	slowBoxTimeout   = time.Minute
	slowBoxCacheTTL  = time.Minute
	slowBoxMaxAnswer = 16 << 20
)

// slowBoxRoutes are box routes answered from boxCache.
var slowBoxRoutes = map[string]bool{
	"POST hardware-upgrade.api.tiiny/api/upgrade/check": true,
}

// boxCache fetches a slow box answer to the end even when the caller
// gives up, and serves the box's own answer to callers for
// slowBoxCacheTTL. Concurrent callers share one fetch. The key includes
// the Authorization header, so one token never sees another's answer.
type boxCache struct {
	mu        sync.Mutex
	entries   map[string]*boxAnswer
	transport http.RoundTripper
}

type boxAnswer struct {
	done   chan struct{}
	at     time.Time
	status int
	header http.Header
	body   []byte
	err    error
}

func (c *boxCache) serve(w http.ResponseWriter, r *http.Request, addr string) {
	key := r.Method + " " + r.Host + r.URL.RequestURI() + " " + r.Header.Get("Authorization")
	c.mu.Lock()
	ans := c.entries[key]
	if ans == nil || ans.err != nil && isClosed(ans.done) || !ans.at.IsZero() && time.Since(ans.at) > slowBoxCacheTTL {
		ans = &boxAnswer{done: make(chan struct{})}
		c.entries[key] = ans
		go ans.fetch(c.transport, r.Clone(context.Background()), addr)
	}
	c.mu.Unlock()
	select {
	case <-ans.done:
	case <-r.Context().Done():
		return
	}
	if ans.err != nil {
		http.Error(w, "device unavailable", http.StatusBadGateway)
		return
	}
	maps.Copy(w.Header(), ans.header)
	w.WriteHeader(ans.status)
	_, _ = w.Write(ans.body)
}

func (a *boxAnswer) fetch(transport http.RoundTripper, r *http.Request, addr string) {
	defer close(a.done)
	ctx, cancel := context.WithTimeout(context.Background(), slowBoxTimeout)
	defer cancel()
	out, err := http.NewRequestWithContext(ctx, r.Method, "http://"+addr+r.URL.RequestURI(), r.Body)
	if err != nil {
		a.err = err
		return
	}
	out.Header = r.Header.Clone()
	out.Host = r.Host
	start := time.Now()
	resp, err := transport.RoundTrip(out)
	if err != nil {
		a.err = err
		log.Printf("box %s %s%s: %v", r.Method, r.Host, r.URL.Path, err)
		return
	}
	defer resp.Body.Close()
	a.body, a.err = io.ReadAll(io.LimitReader(resp.Body, slowBoxMaxAnswer))
	a.status, a.header, a.at = resp.StatusCode, resp.Header.Clone(), time.Now()
	log.Printf("box %s %s%s: %d after %s (cached %s)", r.Method, r.Host, r.URL.Path, resp.StatusCode, time.Since(start).Round(time.Millisecond), slowBoxCacheTTL)
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// bodyReplay gives a fetch its own copy of a buffered request body.
func bodyReplay(r *http.Request) io.ReadCloser {
	if r.GetBody != nil {
		if b, err := r.GetBody(); err == nil {
			return b
		}
	}
	return io.NopCloser(bytes.NewReader(nil))
}
