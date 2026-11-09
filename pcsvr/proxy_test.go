package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyRouting(t *testing.T) {
	release := make(chan struct{})
	box := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: "+r.Host+" "+r.URL.Path+"\n\n")
			w.(http.Flusher).Flush()
			<-release
		case "/missing", "/api/services/apps/metadata":
			http.NotFound(w, r)
		case "/api/v1/sys/wifi/connection_status":
			if r.Host == "api.tiiny" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, "box "+r.Host+" "+r.URL.Path+" ")
		default:
			body, _ := io.ReadAll(r.Body)
			_, _ = io.WriteString(w, "box "+r.Host+" "+r.URL.Path+" "+string(body))
		}
	}))
	defer box.Close()
	addr := strings.TrimPrefix(box.URL, "http://")

	local := http.NewServeMux()
	local.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, "local "+string(body))
	})
	local.HandleFunc("/pcsvr/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "local version")
	})
	device := addr
	srv := httptest.NewServer(proxyOrLocal(local, func() string { return device }))
	defer srv.Close()
	get := func(method, host, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Host = host
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(got)
	}

	// A name on the catalog port reaches the box under its own name, and
	// the first streamed event arrives while the stream is still open.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/chat/completions", nil)
	req.Host = "api.tiiny:18080"
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	line := make(chan string, 1)
	go func() {
		got, _ := bufio.NewReader(resp.Body).ReadString('\n')
		line <- got
	}()
	select {
	case got := <-line:
		if got != "data: api.tiiny /v1/chat/completions\n" {
			t.Fatalf("streamed line %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("streamed event was buffered")
	}
	close(release)
	resp.Body.Close()

	cases := []struct {
		name, host, path, body, want string
		code                         int
	}{
		{"sign-in goes to the box", "auth.api.tiiny:1", "/api/v1/connect", "{}", "box auth.api.tiiny /api/v1/connect {}", 200},
		{"OpenAI API name goes to the box", "openai.api.tiiny", "/v1/models", "", "box openai.api.tiiny /v1/models ", 200},
		{"catalog name the box serves", "p8800.api.tiiny:1", "/api/v1/npu/status", "", "box p8800.api.tiiny /api/v1/npu/status ", 200},
		{"box 404 with a catalog route falls back, body kept", "agent-services.api.tiiny:1", "/missing", "abc", "local abc", 200},
		{"box 404 for a box-only name falls back too", "api.tiiny:1", "/missing", "", "local ", 200},
		{"PC service routes stay local", "127.0.0.1:1", "/pcsvr/version", "", "local version", 200},
		{"store list 404 is answered by the box's app list", "agent-services.api.tiiny:1", "/api/services/apps/metadata", "", "box api.tiiny /api/v1/apps ", 200},
		{"Wi-Fi status 404 on api.tiiny is asked at wifi.api.tiiny", "api.tiiny:1", "/api/v1/sys/wifi/connection_status", "", "box wifi.api.tiiny /api/v1/sys/wifi/connection_status ", 200},
	}
	for _, c := range cases {
		code, got := get(http.MethodPost, c.host, c.path, c.body)
		if code != c.code || got != c.want {
			t.Fatalf("%s: %d %q", c.name, code, got)
		}
	}

	// No box: catalog names are served locally, box-only names are 502.
	device = ""
	if code, got := get(http.MethodGet, "agent-services.api.tiiny:1", "/missing", ""); code != 200 || got != "local " {
		t.Fatalf("no box, catalog name: %d %q", code, got)
	}
	for _, host := range []string{"api.tiiny:1", "auth.api.tiiny:1", "hardware-upgrade.api.tiiny:1"} {
		if code, _ := get(http.MethodGet, host, "/v1/models", ""); code != http.StatusBadGateway {
			t.Fatalf("no box, %s: %d", host, code)
		}
	}
}

func TestListenRetryWaitsForPort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := held.Addr().String()
	go func() {
		time.Sleep(600 * time.Millisecond)
		held.Close()
	}()
	ln, err := listenRetry(func() (net.Listener, error) { return net.Listen("tcp", addr) })
	if err != nil {
		t.Fatalf("bind after release: %v", err)
	}
	ln.Close()
}

func TestSlowBoxAnswerIsCached(t *testing.T) {
	var hits atomic.Int32
	box := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(2 * time.Second)
		_, _ = io.WriteString(w, `{"error_code":"0","host":"`+r.Host+`"}`)
	}))
	defer box.Close()
	addr := strings.TrimPrefix(box.URL, "http://")
	srv := httptest.NewServer(proxyOrLocal(http.NewServeMux(), func() string { return addr }))
	defer srv.Close()
	post := func(timeout time.Duration) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/upgrade/check", nil)
		req.Host = "hardware-upgrade.api.tiiny:1"
		resp, err := srv.Client().Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil
	}
	// The app gives up before the box answers; the fetch goes on.
	if _, err := post(300 * time.Millisecond); err == nil {
		t.Fatal("first call should time out like the app's")
	}
	time.Sleep(2 * time.Second)
	start := time.Now()
	got, err := post(time.Second)
	if err != nil || got != `{"error_code":"0","host":"hardware-upgrade.api.tiiny"}` || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("cached answer %q %v after %s", got, err, time.Since(start))
	}
	if hits.Load() != 1 {
		t.Fatalf("box asked %d times, want 1", hits.Load())
	}
}
