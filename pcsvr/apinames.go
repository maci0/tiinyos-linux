package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// API names. The app's API Key page and its agent integrations give other
// programs http://openai.api.tiiny/v1, http://anthropic.api.tiiny and
// http://ollama.api.tiiny. On Linux they resolve through a marked block in
// /etc/hosts to 127.0.0.1, where pcsvr listens on port 80 with
// cap_net_bind_service and proxies to the box. One pkexec call
// (-api-names) writes both; pcsvr then restarts itself to gain the
// capability.

var apiNames = []string{"openai.api.tiiny", "anthropic.api.tiiny", "ollama.api.tiiny"}

const (
	hostsPath       = "/etc/hosts"
	hostsBegin      = "# tiinyos api names begin"
	hostsEnd        = "# tiinyos api names end"
	apiNamesAddr    = "127.0.0.1:80"
	capNetBind      = 10         // CAP_NET_BIND_SERVICE
	vfsCapRevision2 = 0x02000000 // VFS_CAP_REVISION_2
	vfsCapEffective = 0x1        // VFS_CAP_FLAGS_EFFECTIVE
	restartDelay    = 200 * time.Millisecond
)

// hostsBlock is the marked /etc/hosts block for apiNames.
func hostsBlock() string {
	return hostsBegin + "\n127.0.0.1 " + strings.Join(apiNames, " ") + "\n" + hostsEnd + "\n"
}

// withHostsBlock returns hosts with any earlier block replaced by the
// current one, appended at the end.
func withHostsBlock(hosts string) string {
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
	return out + hostsBlock()
}

// netBindCapability is the security.capability xattr value granting
// CAP_NET_BIND_SERVICE as permitted and effective (vfs_cap_data v2).
func netBindCapability() []byte {
	b := make([]byte, 20)
	binary.LittleEndian.PutUint32(b[0:], vfsCapRevision2|vfsCapEffective)
	binary.LittleEndian.PutUint32(b[4:], 1<<capNetBind)
	return b
}

// configureAPINames runs as root (pkexec): it writes the hosts block and
// gives this executable, and no other file, the port 80 capability.
func configureAPINames() error {
	if os.Geteuid() != 0 {
		return errors.New("-api-names must run as root")
	}
	old, err := os.ReadFile(hostsPath)
	if err != nil {
		return err
	}
	if updated := withHostsBlock(string(old)); updated != string(old) {
		// In place, not by rename: /etc/hosts may be a bind mount.
		if err := os.WriteFile(hostsPath, []byte(updated), 0o644); err != nil {
			return err
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Setxattr(self, "security.capability", netBindCapability(), 0)
}

type apiNamesState struct {
	mu    sync.Mutex
	state string
	err   string
}

func (a *apiNamesState) set(state string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state, a.err = state, ""
	if err != nil {
		a.err = err.Error()
		log.Printf("api names: %s: %v", state, err)
		return
	}
	log.Printf("api names: %s", state)
}

// namesInHosts reports whether every API name has a line in hosts,
// comments aside: set up by hand or by another tool.
func namesInHosts(hosts string) bool {
	listed := map[string]bool{}
	for _, line := range strings.Split(hosts, "\n") {
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

// serve listens on apiNamesAddr when the hosts block is current and
// pcsvr has the capability. Names that /etc/hosts already lists some
// other way are left to that entry (state external, no prompt).
func (a *apiNamesState) serve(handler http.Handler) {
	data, err := os.ReadFile(hostsPath)
	hosts := string(data)
	switch {
	case err != nil:
		a.set("needs_admin", err)
		return
	case !strings.Contains(hosts, hostsBlock()) && namesInHosts(hosts):
		a.set("external", nil)
		return
	case !strings.Contains(hosts, hostsBlock()):
		a.set("needs_admin", errors.New("the /etc/hosts block is missing"))
		return
	}
	listener, err := net.Listen("tcp", apiNamesAddr)
	if err != nil {
		a.set("needs_admin", err)
		return
	}
	a.set("ready", nil)
	log.Printf("api names: listening on %s", apiNamesAddr)
	onlyNames := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Port 80 serves only the API names, never the local endpoints.
		if !slices.Contains(apiNames, strings.ToLower(hostOnly(r.Host))) {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: onlyNames, ReadHeaderTimeout: 5 * time.Second}
	if err := server.Serve(listener); err != nil {
		log.Printf("api names: %v", err)
	}
}

func (a *apiNamesState) status() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"state": a.state, "names": apiNames, "error": a.err}
}

// setupWithAdmin runs the root helper through pkexec, then re-executes
// pcsvr (same PID) so the new file capability takes effect.
func (a *apiNamesState) setupWithAdmin() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if out, err := exec.Command("pkexec", self, "-api-names").CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("api names: configured, restarting")
	go func() {
		time.Sleep(restartDelay) // let the HTTP reply go out
		log.Fatal(syscall.Exec(self, os.Args, os.Environ()))
	}()
	return nil
}

// hostOnly is host without a port.
func hostOnly(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return host
}
