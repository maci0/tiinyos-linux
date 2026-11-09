package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// API names. The app's API Key page and its agent integrations give other
// programs http://openai.api.tiiny/v1, http://anthropic.api.tiiny and
// http://ollama.api.tiiny. The box answers those names itself on its port
// 80, so on Linux a marked block in /etc/hosts points them at the box's
// current address. One pkexec call (-api-names <address>) writes it;
// pcsvr asks again only when discovery finds the box at another address.

var apiNames = []string{"openai.api.tiiny", "anthropic.api.tiiny", "ollama.api.tiiny"}

const (
	hostsPath          = "/etc/hosts"
	hostsBegin         = "# tiinyos api names begin"
	hostsEnd           = "# tiinyos api names end"
	apiNamesCheckEvery = 2 * time.Second
)

// hostsBlock is the marked /etc/hosts block pointing apiNames at ip.
func hostsBlock(ip string) string {
	return hostsBegin + "\n" + ip + " " + strings.Join(apiNames, " ") + "\n" + hostsEnd + "\n"
}

// withHostsBlock returns hosts with any earlier block replaced by one for
// ip, appended at the end.
func withHostsBlock(hosts, ip string) string {
	var kept []string
	inside := false
	for _, line := range strings.SplitAfter(hosts, "\n") {
		switch strings.TrimSpace(line) {
		case hostsBegin:
			inside = true
			continue
		case hostsEnd:
			inside = false
			continue
		}
		if !inside && line != "" {
			kept = append(kept, line)
		}
	}
	out := strings.Join(kept, "")
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + hostsBlock(ip)
}

// blockAddress is the address in hosts' block, or "" without a block.
func blockAddress(hosts string) string {
	_, rest, ok := strings.Cut(hosts, hostsBegin+"\n")
	if !ok {
		return ""
	}
	line, _, _ := strings.Cut(rest, "\n")
	fields := strings.Fields(line)
	if len(fields) < 1+len(apiNames) || net.ParseIP(fields[0]) == nil {
		return ""
	}
	return fields[0]
}

// namesInHosts reports whether every API name has a line in hosts outside
// the block, comments aside: set up by hand or by another tool.
func namesInHosts(hosts string) bool {
	listed := map[string]bool{}
	inside := false
	for _, line := range strings.Split(hosts, "\n") {
		switch strings.TrimSpace(line) {
		case hostsBegin:
			inside = true
			continue
		case hostsEnd:
			inside = false
			continue
		}
		if inside {
			continue
		}
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		for i := 1; i < len(fields); i++ {
			listed[strings.ToLower(fields[i])] = true
		}
	}
	for _, name := range apiNames {
		if !listed[name] {
			return false
		}
	}
	return true
}

// configureAPINames runs as root (pkexec) and points the block at ip.
func configureAPINames(ip string) error {
	if os.Geteuid() != 0 {
		return errors.New("-api-names must run as root")
	}
	if parsed := net.ParseIP(ip); parsed == nil || parsed.To4() == nil {
		return fmt.Errorf("not an IPv4 address: %q", ip)
	}
	old, err := os.ReadFile(hostsPath)
	if err != nil {
		return err
	}
	if updated := withHostsBlock(string(old), ip); updated != string(old) {
		// In place, not by rename: /etc/hosts may be a bind mount.
		return os.WriteFile(hostsPath, []byte(updated), 0o644)
	}
	return nil
}

// apiNamesState is the API names state: ready (block points at the box),
// external (names listed outside the block; never prompts), no_box,
// needs_admin (no block) or needs_update (block points elsewhere).
type apiNamesState struct {
	mu    sync.Mutex
	state string
	err   string
}

// apiNamesFor derives the state from /etc/hosts and the box address.
func apiNamesFor(hosts, box string) (string, string) {
	block := blockAddress(hosts)
	switch {
	case block == "" && namesInHosts(hosts):
		return "external", ""
	case box == "":
		return "no_box", ""
	case block == "":
		return "needs_admin", "the /etc/hosts block is missing"
	case block != box:
		return "needs_update", fmt.Sprintf("the names point to %s, the box is at %s", block, box)
	}
	return "ready", ""
}

// watch re-derives the state every apiNamesCheckEvery until ctx ends.
func (a *apiNamesState) watch(ctx context.Context, box func() string) {
	for {
		data, err := os.ReadFile(hostsPath)
		state, why := apiNamesFor(string(data), box())
		if err != nil {
			state, why = "needs_admin", err.Error()
		}
		a.mu.Lock()
		if state != a.state {
			log.Printf("api names: %s %s", state, why)
		}
		a.state, a.err = state, why
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(apiNamesCheckEvery):
		}
	}
}

func (a *apiNamesState) status() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"state": a.state, "names": apiNames, "error": a.err}
}

// setupWithAdmin points the block at box through pkexec.
func (a *apiNamesState) setupWithAdmin(box string) error {
	if box == "" {
		return errors.New("no box found")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if out, err := exec.Command("pkexec", self, "-api-names", box).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("api names: pointed at %s", box)
	return nil
}
