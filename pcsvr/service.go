package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The shipping pcsvr registers one handler file per route. The bodies
// are stripped from the binary. These handlers implement the routes the
// Electron client calls and the routes named by those handler files.
// Response status is "ok" because the client throws on anything else.

const (
	// generateTemporaryAuthKey loads 0x45d964b800 nanoseconds, which is
	// 300 seconds. The signing key itself is not a literal in that function.
	tempKeyTTL = 300 * time.Second
	// checkNonce multiplies the stored window by 0x3b9aca00 nanoseconds
	// and falls back to 300 seconds when that window is unset.
	debugNonceTTL = 300 * time.Second
	requestLimit  = 100
	eventLimit    = 100
)

// debugRootPEM is the single PEM embedded in the Apple tiiny_svr.
// Subject and issuer are O=Tiiny Dev Center, CN=Tiiny Dev Center.
// The key is ECDSA P-256 and the certificate is a CA.
var debugRootPEM = `-----BEGIN CERTIFICATE-----
MIIBsDCCAVagAwIBAgIRAKkR6+yJMjQ1SJigQZVg4F0wCgYIKoZIzj0EAwIwNjEZ
MBcGA1UEChMQVGlpbnkgRGV2IENlbnRlcjEZMBcGA1UEAxMQVGlpbnkgRGV2IENl
bnRlcjAeFw0yNjA3MTcxMjQ4MzBaFw0zNjA3MTcxMjUzMzBaMDYxGTAXBgNVBAoT
EFRpaW55IERldiBDZW50ZXIxGTAXBgNVBAMTEFRpaW55IERldiBDZW50ZXIwWTAT
BgcqhkjOPQIBBggqhkjOPQMBBwNCAATF8yGbJjML+Cr19dWwKfIjvsbRd/+DYbYA
G4flx3awO4cWFKi1vDvy9Px2bcFdnpY9VHRw5K1sUI0lHXx7YiKXo0UwQzAOBgNV
HQ8BAf8EBAMCAQYwEgYDVR0TAQH/BAgwBgEB/wIBADAdBgNVHQ4EFgQU7fjgtUGi
cl22Hi4I0bpQl8bA4LwwCgYIKoZIzj0EAwIDSAAwRQIgPVXU5DLHbbY0+svr0iYz
U2OjlAvSQTh3rqkDopmMupcCIQC9/Wx/3bVhOfzfQxpOl4av87/g3SWT3Rsa0nRQ
zdh1Zw==
-----END CERTIFICATE-----`

type authRecord struct {
	DeviceID   string    `json:"device_id"`
	AuthKey    string    `json:"auth_key"`
	UserID     string    `json:"user_id,omitempty"`
	AgentID    string    `json:"agent_id,omitempty"`
	DeviceName string    `json:"device_name,omitempty"`
	AutoEject  bool      `json:"auto_eject"`
	Updated    time.Time `json:"updated"`
}

type tempAuthKey struct {
	Token    string    `json:"token"`
	DeviceID string    `json:"device_id"`
	Expires  time.Time `json:"expires"`
}

type loggedEvent struct {
	ID        int64  `json:"id"`
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Category  string `json:"category"`
	Event     string `json:"event"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
}

type serviceState struct {
	mu        sync.Mutex
	keys      map[string]authRecord
	temps     []tempAuthKey
	records   []map[string]any
	usbBlock  bool
	wireguard bool
	capture   bool
	unlocked  bool
	pprofOn   bool
	nonces    map[string]time.Time
	events    []loggedEvent
}

func newServiceState() *serviceState {
	return &serviceState{keys: map[string]authRecord{}, nonces: map[string]time.Time{}}
}

func (s *state) service() *serviceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc == nil {
		s.svc = newServiceState()
	}
	return s.svc
}

func registerService(mux *http.ServeMux, s *state) {
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, versionBody())
	})
	mux.HandleFunc("GET /pcsvr/log", s.listEvents)
	mux.HandleFunc("POST /pcsvr/account/get_temporary_auth_key", s.issueTempKey)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.statusSnapshot())
	})
	mux.HandleFunc("GET /status/stream", s.statusStream)
	mux.HandleFunc("GET /devices_list", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.devicesList())
	})
	mux.HandleFunc("GET /dns/records", s.listDNS)
	mux.HandleFunc("POST /dns/resolve", s.resolveDNS)
	mux.HandleFunc("POST /dns/update", s.updateDNS)
	// RegisterHandlers builds POST /dns/update_record with the same
	// UpdateRecord closure used by POST /dns/update.
	mux.HandleFunc("POST /dns/update_record", s.updateDNS)
	mux.HandleFunc("GET /account/list_auth_keys", s.listAuthKeys)
	mux.HandleFunc("GET /account/auth_keys", s.listAuthKeys)
	mux.HandleFunc("GET /account/list_temporary_auth_keys", s.listTempKeys)
	mux.HandleFunc("GET /account/temporary_auth_keys", s.listTempKeys)
	mux.HandleFunc("POST /account/delete_auth_key", s.deleteAuthKey)
	mux.HandleFunc("DELETE /account/auth_keys", s.deleteAuthKey)
	mux.HandleFunc("POST /account/delete_temporary_auth_keys", s.deleteTempKeys)
	mux.HandleFunc("DELETE /account/temporary_auth_keys", s.deleteTempKeys)
	mux.HandleFunc("POST /account/get_temporary_auth_key", s.issueTempKey)
	mux.HandleFunc("POST /account/update_usb_auto_eject", s.updateUSBAutoEject)
	mux.HandleFunc("POST /account/update_wireguard", s.updateWireGuard)
	mux.HandleFunc("GET /log", s.listEvents)
	mux.HandleFunc("GET /requests", s.listRequests)
	mux.HandleFunc("GET /debug/requests", s.listRequests)
	mux.HandleFunc("POST /requests/capture", s.captureRequests)
	mux.HandleFunc("POST /debug/requests/capture", s.captureRequests)
	mux.HandleFunc("POST /requests/clear", s.clearRequests)
	mux.HandleFunc("DELETE /debug/requests", s.clearRequests)
	mux.HandleFunc("POST /debug/unlock", s.debugUnlock)
	mux.HandleFunc("GET /debug/pprof", s.pprofStatus)
	mux.HandleFunc("POST /debug/pprof", s.pprofToggle)
	mux.HandleFunc("GET /debug/guard/status", s.guardStatus)
	mux.HandleFunc("GET /debug/guard/probe", s.guardStatus)
	mux.HandleFunc("POST /debug/guard/probe", s.guardStatus)
	mux.HandleFunc("POST /debug/guard/standdown", s.guardStandDown)
	mux.HandleFunc("GET /docs/swagger", s.swagger)
	// The docs registration also stores GET /docs on the same handler.
	mux.HandleFunc("GET /docs", s.swagger)
	mux.HandleFunc("POST /usb-storage", s.usbStorage)
	mux.HandleFunc("GET /usb-storage", s.usbStorageStatus)
	mux.HandleFunc("GET /webui", s.webUI)
	// The account table at file 0xea57c0 stores the pointer at file
	// 0x10a04f0 (writer 0x6d4070) into the six OPTIONS handler slots
	// +0x160 through +0x228. The five after /account/get_temporary_auth_key
	// are +0x188 /account/temporary_auth_keys, +0x1b0
	// /account/update_auth_key, +0x1d8 /account/auth_keys, +0x200
	// /account/update_usb_auto_eject, and +0x228 /account/update_wireguard.
	// The 28-route builder and RegisterHandlers store that same pointer
	// on the debug, device, status, version, log, and DNS OPTIONS records.
	// The writer sets the six CORS headers and returns 204.
	for _, path := range []string{
		"/account/get_temporary_auth_key",
		"/account/temporary_auth_keys",
		"/account/update_auth_key",
		"/account/auth_keys",
		"/account/update_usb_auto_eject",
		"/account/update_wireguard",
		"/debug/requests",
		"/debug/requests/capture",
		"/debug/unlock",
		"/debug/pprof",
		"/debug/guard/status",
		"/debug/guard/standdown",
		"/devices_list",
		"/devices_list/ws",
		"/select_device",
		"/status",
		"/status/stream",
		"/version",
		"/log",
		"/dns/update_record",
		"/dns/records",
		"/dns/resolve",
	} {
		mux.HandleFunc("OPTIONS "+path, pprofOptions)
	}
}

func pprofOptions(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Set("Access-Control-Expose-Headers", "Content-Length")
	h.Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

// corsOptions is corsOptionsHandler. The writer at 0x6d3670 stores
// status 200. It names the Content-Type header, but the value passed
// to that call is not a literal in the writer.
func corsOptions(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *state) statusSnapshot() map[string]any {
	svc := s.service()
	svc.mu.Lock()
	usbOn := svc.usbBlock
	wgOn := svc.wireguard
	dnsOn := s.cfg.DNS
	svc.mu.Unlock()
	s.mu.Lock()
	tracked := len(s.authKeys)
	s.mu.Unlock()
	records := len(s.dnsRecords())
	// StatusSnapshotResp, in field order: updated_at, service_status,
	// tracked_auth_devices, version, dns, ssh_proxy, usb_auto_eject,
	// wireguard, usb_networks. The nested objects are the structs at
	// 0x1058cd8, 0x1073c28, 0x1067570, 0x104be18, and 0x1067450.
	version := versionBody()
	usbMsg := "Queried USB auto eject status"
	if usbOn {
		usbMsg = "usb auto eject is not supported on this platform"
	}
	// The snapshot copies the constant at 0xe9b640: status "error",
	// error_code 50020, wireguard_status "Unavailable", msg
	// "wireguard manager is unavailable". When the manager at
	// svc+0x618 is present it overwrites enabled, supported,
	// installed, and running from the manager. PCSVR_WIREGUARD_SCRIPT
	// stands in for that manager.
	wgMsg := "wireguard manager is unavailable"
	wgCode := 50020
	wgStatusText := "Unavailable"
	wgStatus := "error"
	wgSupported := false
	if runtime.GOOS == "darwin" || os.Getenv("PCSVR_WIREGUARD_SCRIPT") != "" || wireGuardScript() != "" {
		// The manager-present constant at 0xe9b680 is status "ok",
		// error_code 0, and an empty msg. The four bools are then
		// overwritten from the manager.
		wgSupported = true
		wgStatus = "ok"
		wgCode = 0
		wgStatusText = ""
		wgMsg = ""
	}
	return map[string]any{
		"updated_at":           time.Now().UTC().Format(time.RFC3339),
		"service_status":       "running",
		"tracked_auth_devices": tracked,
		"version":              version,
		"dns": map[string]any{
			"status":                       "ok",
			"error_code":                   0,
			"enabled":                      dnsOn,
			"running":                      dnsOn,
			"domain":                       s.cfg.Domain,
			"listen_address":               "127.0.0.1:60053",
			"compatibility_listen_address": "",
			"resolver_configured":          dnsOn,
			"default_ipv4":                 "127.0.0.1",
			"record_count":                 records,
			"msg":                          "",
		},
		"ssh_proxy": map[string]any{
			"status":         "ok",
			"error_code":     0,
			"enabled":        s.cfg.SSH,
			"running":        s.cfg.SSH,
			"listen_address": "127.0.0.1:60022",
			"target_port":    22,
			"command":        sshCommand(s.targetIP()),
			"msg":            "",
		},
		"usb_auto_eject": map[string]any{
			"status":     "ok",
			"error_code": 0,
			"enabled":    usbOn,
			"supported":  false,
			"msg":        usbMsg,
		},
		"wireguard": map[string]any{
			"status":           wgStatus,
			"error_code":       wgCode,
			"enabled":          wgOn && wgSupported,
			"supported":        wgSupported,
			"installed":        false,
			"running":          wgOn && wgSupported,
			"wireguard_status": wgStatusText,
			"msg":              wgMsg,
		},
		"usb_networks": []any{},
	}
}

// statusStream is GET /status/stream. The binary's event text is
// "event: status". The first frame is that event plus the same snapshot
// GET /status returns.
func (s *state) statusStream(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	key := websocketAccept(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		_, _ = rw.WriteString("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		_ = rw.Flush()
		return
	}
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + key + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		return
	}
	frame := s.statusSnapshot()
	frame["event"] = "status"
	_ = writeWSText(conn, mustJSON(frame))
}

func versionBody() map[string]string {
	return map[string]string{
		"version":    versionString(),
		"build_time": "",
		"git_commit": "linux-port",
		"go_version": "go",
		"platform":   "linux/amd64",
		"status":     "running",
	}
}

func (s *state) noteEvent(event string, data any) {
	category := "service"
	switch {
	case event == "status_queried", event == "manager_unavailable", event == "unsupported", event == "invalid_action", event == "update_failed", strings.Contains(event, "wire"), strings.HasPrefix(event, "start_"), strings.HasPrefix(event, "stop_"), strings.HasPrefix(event, "install_"):
		category = "wireguard"
	case strings.Contains(event, "eject"), strings.Contains(event, "usb"), strings.Contains(event, "drive"), strings.Contains(event, "block"):
		category = "usb"
	case strings.Contains(event, "guard"), event == "stood_down":
		category = "guard"
	case strings.Contains(event, "record"), strings.Contains(event, "dns"):
		category = "dns"
	case strings.Contains(event, "auth"), strings.Contains(event, "key"):
		category = "auth"
	case strings.Contains(event, "web"), strings.Contains(event, "connect"):
		category = "webui"
	}
	svc := s.service()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	msg, details := "", data
	if text, ok := data.(string); ok {
		msg, details = text, nil
	}
	level := "info"
	if event == "manager_unavailable" {
		level = "warn"
	}
	// The invalid-action log at 0x6be83c and the unsupported log at
	// 0x6be970 both store the 4-byte level "warn".
	if event == "invalid_action" || event == "unsupported" {
		level = "warn"
	}
	// The save-failure log at 0x6bcab4 and the delete-failure log at
	// 0x6b9264 both store the 5-byte level "error".
	if event == "update_auth_key_failed" || event == "delete_auth_key_failed" {
		level = "error"
	}
	// The mismatch log at 0x6bcfa4 stores the 4-byte level "warn".
	if event == "storage_inconsistency" {
		level = "warn"
	}
	svc.events = append(svc.events, loggedEvent{
		ID:        int64(len(svc.events) + 1),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Category:  category,
		Event:     event,
		Message:   msg,
		Details:   details,
	})
	if len(svc.events) > eventLimit {
		svc.events = svc.events[len(svc.events)-eventLimit:]
	}
}

func (s *state) listDNS(w http.ResponseWriter, r *http.Request) {
	records := s.dnsRecords()
	// List response fields: status, error_code, domain, default_ipv4,
	// record_count, records, msg.
	writeJSON(w, map[string]any{
		"status":       "ok",
		"error_code":   0,
		"domain":       s.cfg.Domain,
		"default_ipv4": "127.0.0.1",
		"record_count": len(records),
		"records":      records,
		"msg":          "",
	})
}

func (s *state) resolveDNS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string   `json:"name"`
		Hosts []string `json:"hosts"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, map[string]any{"status": "error", "error_code": 40020, "msg": "hosts are required"})
			return
		}
	}
	if q := strings.TrimSpace(r.URL.Query().Get("name")); q != "" && body.Name == "" && len(body.Hosts) == 0 {
		body.Hosts = []string{q}
	}
	if body.Name != "" {
		body.Hosts = append(body.Hosts, body.Name)
	}
	var hosts []string
	for _, host := range body.Hosts {
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	// ResolveRecords returns 40020 when the host list is empty. The
	// message is the 18-byte slice "hosts are required".
	if len(hosts) == 0 {
		writeJSON(w, map[string]any{"status": "error", "error_code": 40020, "msg": "hosts are required"})
		return
	}
	if !s.cfg.DNS {
		writeJSON(w, map[string]any{"status": "error", "error_code": 50030, "msg": "dns manager is unavailable"})
		return
	}
	records := s.dnsRecords()
	var matched []map[string]any
	for _, host := range hosts {
		for _, rec := range records {
			if rec["fqdn"] == host {
				matched = append(matched, rec)
			}
		}
	}
	if matched == nil {
		matched = []map[string]any{}
	}
	writeJSON(w, map[string]any{"status": "ok", "error_code": 0, "records": matched})
}

func (s *state) updateDNS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		FQDN   string `json:"fqdn"`
		Host   string `json:"host"`
		IPv4   string `json:"ipv4"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil {
		writeJSON(w, dnsUpdateBody(40011, "type mismatch for field \"body\"", "", 0, nil))
		return
	}
	if !s.cfg.DNS {
		writeJSON(w, dnsUpdateBody(50031, "dns server is disabled", "", 0, nil))
		return
	}
	action := strings.TrimSpace(strings.ToLower(body.Action))
	if action == "" {
		action = "set"
	}
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(body.FQDN)), ".")
	if host == "" {
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(body.Host)), ".")
	}
	// UpdateRecord compares the action with "set" (3 bytes) and
	// "delete" (6 bytes). Anything else is error 40012 and the
	// 28-byte sentence "action must be set or delete".
	if action != "set" && action != "delete" {
		writeJSON(w, dnsUpdateBody(40012, "action must be set or delete", action, 0, nil))
		return
	}
	if host == "" {
		writeJSON(w, dnsUpdateBody(40010, "host is required", action, 0, nil))
		return
	}
	if action == "set" && strings.TrimSpace(body.IPv4) == "" {
		writeJSON(w, dnsUpdateBody(40011, "value is required for set action", action, 0, nil))
		return
	}
	svc := s.service()
	svc.mu.Lock()
	if action == "delete" {
		kept := svc.records[:0]
		found := false
		for _, existing := range svc.records {
			if existing["fqdn"] == host {
				found = true
				continue
			}
			kept = append(kept, existing)
		}
		svc.records = kept
		count := len(svc.records)
		svc.mu.Unlock()
		if !found {
			s.noteEvent("record_delete_noop", host)
			writeJSON(w, dnsUpdateBody(0, "Delete DNS record skipped: host not present", action, count, nil))
			return
		}
		s.noteEvent("record_deleted_via_api", host)
		writeJSON(w, dnsUpdateBody(0, "Deleted DNS record", action, count, nil))
		return
	}
	rec := map[string]any{"fqdn": host, "ipv4": body.IPv4, "ttl": dnsTTL}
	replaced := false
	for i, existing := range svc.records {
		if existing["fqdn"] == host {
			svc.records[i] = rec
			replaced = true
		}
	}
	if !replaced {
		svc.records = append(svc.records, rec)
	}
	count := len(svc.records)
	svc.mu.Unlock()
	s.noteEvent("record_updated_via_api", rec)
	writeJSON(w, dnsUpdateBody(0, "Updated DNS record", action, count, rec))
}

// dnsUpdateBody is the update response struct: status, error_code,
// action, record_count, record, msg.
func dnsUpdateBody(code int, msg, action string, count int, record any) map[string]any {
	status := "ok"
	if code != 0 {
		status = "error"
	}
	return map[string]any{
		"status":       status,
		"error_code":   code,
		"action":       action,
		"record_count": count,
		"record":       record,
		"msg":          msg,
	}
}

// authKeyPreview is the function at 0x6bb8f0. A key of 8 bytes or fewer
// is the 4-byte string "****". A longer key keeps every byte except the
// last four and joins those four bytes.
func authKeyPreview(authKey string) string {
	if len(authKey) <= 8 {
		return "****"
	}
	return authKey[:len(authKey)-4] + "****"
}

func (s *state) listAuthKeys(w http.ResponseWriter, r *http.Request) {
	svc := s.service()
	svc.mu.Lock()
	keys := make(map[string]string, len(svc.keys))
	for id, rec := range svc.keys {
		keys[id] = rec.AuthKey
	}
	svc.mu.Unlock()
	// The list also walks auth_data for names ending in ".json". A file
	// supplies a device id that is not already in memory.
	entries, _ := os.ReadDir(authDir())
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if _, ok := keys[id]; ok {
			continue
		}
		if info, ok := loadAuthFile(id); ok {
			keys[id] = info.AuthKey
		}
	}
	now := time.Now()
	out := make([]map[string]any, 0, len(keys))
	for id, authKey := range keys {
		info, _ := loadAuthFile(id)
		expire := ""
		expired := false
		if !info.ExpireTime.IsZero() {
			expire = info.ExpireTime.UTC().Format(time.RFC3339)
			expired = info.ExpireTime.Before(now)
		}
		out = append(out, map[string]any{
			"device_id":   id,
			"expire_time": expire,
			"expired":     expired,
			"key_preview": authKeyPreview(authKey),
		})
	}
	writeJSON(w, map[string]any{"status": "ok", "keys": out})
}

func (s *state) issueTempKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID string `json:"device_id"`
		AgentID  string `json:"agent_id"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body)
	if strings.TrimSpace(body.DeviceID) == "" && strings.TrimSpace(body.AgentID) == "" {
		s.noteEvent("temporary_auth_key_rejected", "empty")
		writeJSON(w, map[string]any{"status": "error", "error_code": 41001, "msg": "Rejected temporary auth key request: empty request"})
		return
	}
	if strings.TrimSpace(body.DeviceID) == "" || strings.TrimSpace(body.AgentID) == "" {
		s.noteEvent("temporary_auth_key_rejected", "missing")
		writeJSON(w, map[string]any{"status": "error", "error_code": 41001, "msg": "Rejected temporary auth key request because agent_id or device_id is missing"})
		return
	}
	s.mu.Lock()
	authKey, known := s.authKeys[body.DeviceID]
	s.mu.Unlock()
	var expireAt time.Time
	if !known {
		// Memory has no key. The call at 0x6ba03c loads
		// auth_data/<device>.json. A file error falls through to the
		// missing-key response. A loaded record supplies the key and
		// its expire_time.
		if info, ok := loadAuthFile(body.DeviceID); ok {
			authKey = info.AuthKey
			expireAt = info.ExpireTime
			known = true
			s.mu.Lock()
			s.authKeys[body.DeviceID] = authKey
			s.mu.Unlock()
		}
	}
	if known && expireAt.IsZero() {
		if info, ok := loadAuthFile(body.DeviceID); ok {
			expireAt = info.ExpireTime
		}
	}
	if !known {
		// The missing-key return at 0x6baa98 stores code 50001 and then
		// concatenates the 43-byte prefix with the device id. The log
		// data is the 61-byte sentence and the event is
		// temporary_auth_key_failed. The 58-byte "expired" sentence is a
		// different branch.
		s.noteEvent("temporary_auth_key_failed", "Temporary auth key request failed because auth key is missing")
		writeJSON(w, map[string]any{
			"status":     "error",
			"error_code": 50001,
			"msg":        "PC Service don't have auth key for device: " + body.DeviceID,
		})
		return
	}
	// The loaded auth record's ExpireTime is a time.Time. The check at
	// 0x6bb0f0 is time.Now then time.Time.Before. A zero time is the nil
	// path and skips the check. A time before now returns 50001 with the
	// 29-byte prefix plus the device id. The log data is the 58-byte
	// expired sentence.
	if !expireAt.IsZero() && expireAt.Before(time.Now()) {
		s.noteEvent("temporary_auth_key_failed", "Temporary auth key request failed because auth key expired")
		writeJSON(w, map[string]any{
			"status":     "error",
			"error_code": 50001,
			"msg":        "Auth key expired for device: " + body.DeviceID,
		})
		return
	}
	expires := time.Now().UTC().Add(tempKeyTTL)
	// The lookup at 0x6bab90 builds "%s|%s|%d|%s|%s" and then prefixes
	// the 4 bytes "tak|". The success path stores that string.
	token := tempKeyData(authKey, time.Now())
	svc := s.service()
	svc.mu.Lock()
	svc.temps = append(svc.temps, tempAuthKey{Token: token, DeviceID: body.DeviceID, Expires: expires})
	svc.mu.Unlock()
	// generateTemporaryAuthKey logs the 54-byte sentence after the
	// token is stored. The HMAC key is not a literal in that function.
	s.noteEvent("temporary_auth_key_generated", fmt.Sprintf("Generated temporary auth key for agent: %s, device: %s", body.AgentID, body.DeviceID))
	// The success object at 0x103bf38 is status, error_code, data, msg.
	// The writer at 0x6ba818 stores "ok", error_code 0, the token in
	// data, and an empty msg. "Generated temporary auth key" is the
	// log line.
	writeJSON(w, map[string]any{
		"status":     "ok",
		"error_code": 0,
		"data":       token,
		"msg":        "",
	})
}

func (s *state) listTempKeys(w http.ResponseWriter, r *http.Request) {
	svc := s.service()
	svc.mu.Lock()
	temps := append([]tempAuthKey(nil), svc.temps...)
	svc.mu.Unlock()
	// Each element is the 56-byte summary built at 0x6bb750: device_id,
	// expire_time formatted with RFC3339, expired from time.Before, and
	// key_preview from 0x6bb8f0. ListTemporaryAuthKeys at 0x6bb264
	// branches to the writer at 0x6bb2f4 when the service context is nil.
	// That writer stores status "error", code 50010, and the 30-byte
	// sentence at 0x755e44. This process always has a context, so the
	// HTTP handler does not take that branch.
	now := time.Now()
	out := make([]map[string]any, 0, len(temps))
	for _, key := range temps {
		out = append(out, map[string]any{
			"device_id":   key.DeviceID,
			"expire_time": key.Expires.UTC().Format(time.RFC3339),
			"expired":     key.Expires.Before(now),
			"key_preview": authKeyPreview(key.Token),
		})
	}
	writeJSON(w, map[string]any{"status": "ok", "error_code": 0, "keys": out})
}

func (s *state) deleteAuthKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID string `json:"device_id"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	// An empty body is the branch at 0x6b9458: status "error",
	// error_code 40001, and the 21-byte msg "device_id is required".
	if len(strings.TrimSpace(string(raw))) == 0 || json.Unmarshal(raw, &body) != nil || strings.TrimSpace(body.DeviceID) == "" {
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "device_id is required"})
		return
	}
	svc := s.service()
	svc.mu.Lock()
	_, stored := svc.keys[body.DeviceID]
	if stored {
		delete(svc.keys, body.DeviceID)
	}
	svc.mu.Unlock()
	s.mu.Lock()
	_, live := s.authKeys[body.DeviceID]
	if live {
		delete(s.authKeys, body.DeviceID)
	}
	s.mu.Unlock()
	fileGone := removeAuthFile(body.DeviceID)
	// The failure path at 0x6b92d8 stores status "error", error_code
	// 50011, and the error text. The log line is the 25-byte
	// "Failed to delete auth key", event "delete_auth_key_failed".
	if !stored && !live && !fileGone {
		msg := "failed to remove auth file for device " + body.DeviceID
		s.noteEvent("delete_auth_key_failed", "Failed to delete auth key")
		writeJSON(w, map[string]any{"status": "error", "error_code": 50011, "msg": msg})
		return
	}
	// Success at 0x6b942c stores status "ok", error_code 0, and an
	// empty msg. "Auth key deleted" is the log line.
	s.noteEvent("auth_key_deleted", "Auth key deleted")
	writeJSON(w, map[string]any{"status": "ok", "error_code": 0, "msg": ""})
}

func (s *state) deleteTempKeys(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID string `json:"device_id"`
		Token    string `json:"token"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "type mismatch for field \"body\""})
			return
		}
	}
	if len(strings.TrimSpace(string(raw))) > 0 && strings.TrimSpace(body.DeviceID) == "" && strings.TrimSpace(body.Token) == "" {
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "device_id is required"})
		return
	}
	svc := s.service()
	svc.mu.Lock()
	kept := svc.temps[:0]
	removed := 0
	for _, key := range svc.temps {
		// The filter at 0x6b3580 drops a key when the request names
		// that device id or that token. An empty body names neither,
		// so the count stays zero and the keys stay.
		drop := (body.DeviceID != "" && key.DeviceID == body.DeviceID) || (body.Token != "" && key.Token == body.Token)
		if drop {
			removed++
			continue
		}
		kept = append(kept, key)
	}
	svc.temps = kept
	svc.mu.Unlock()
	// The success log at 0x6b9810 stores level "info", category "auth",
	// event "temporary_auth_keys_deleted" (0x75235a, 27 bytes), and the
	// 27-byte message "Temporary auth keys deleted". The HTTP body is
	// the 48-byte struct at 0x103c0b8: status, error_code, removed, msg.
	// The success writer at 0x6b985c stores status "ok", error code 0,
	// removed from the filter, and an empty msg.
	s.noteEvent("temporary_auth_keys_deleted", "Temporary auth keys deleted")
	writeJSON(w, map[string]any{"status": "ok", "error_code": 0, "removed": removed, "msg": ""})
}

func (s *state) updateUSBAutoEject(w http.ResponseWriter, r *http.Request) {
	// The handler reads the eject monitor at svc+0x600 before it looks
	// at the action. Both nil checks branch to the same writer: status
	// "error", error_code 50010, enabled false, supported false, and
	// the 37-byte sentence. Linux has no monitor, so the request body
	// is not read.
	s.noteEvent("auto_eject_unavailable", "USB auto eject monitor is unavailable")
	writeJSON(w, usbAutoEjectBody(50010, "usb auto eject monitor is unavailable", false))
}

// usbAutoEjectBody is the 40-byte UpdateUSBAutoEjectResp at type
// 0x104be18: status, error_code, enabled, supported, msg.
func usbAutoEjectBody(code int, msg string, enabled bool) map[string]any {
	status := "ok"
	if code != 0 {
		status = "error"
	}
	return map[string]any{
		"status":     status,
		"error_code": code,
		"enabled":    enabled,
		"supported":  false,
		"msg":        msg,
	}
}

// sshCommand is the string StatusHandler builds. An empty target is
// empty. A target containing ":" is wrapped in brackets, using the
// 11-byte prefix "ssh tiiny@[" and a closing "]". Any other target
// uses the 10-byte prefix "ssh tiiny@".
func sshCommand(target string) string {
	if target == "" {
		return ""
	}
	if strings.Contains(target, ":") {
		return "ssh tiiny@[" + target + "]"
	}
	return "ssh tiiny@" + target
}

func (s *state) updateWireGuard(w http.ResponseWriter, r *http.Request) {
	// The request type at file 0xfe4598 has one field, action, tagged
	// json "action,optional". The handler loads the manager at entry.
	// Both nil checks at 0x6bde88 and 0x6bde94 branch to 0x6be9e4
	// before the body is read.
	if os.Getenv("PCSVR_WIREGUARD_SCRIPT") == "" && wireGuardScript() == "" {
		s.noteEvent("manager_unavailable", "WireGuard manager is unavailable")
		body := wireGuardBody(50020, "wireguard manager is unavailable", false, false)
		body["enabled"] = false
		body["wireguard_status"] = "Unavailable"
		writeJSON(w, body)
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil || (len(strings.TrimSpace(string(raw))) > 0 && json.Unmarshal(raw, &req) != nil) {
		writeJSON(w, map[string]any{"status": "error", "error_code": 40001, "msg": "type mismatch for field \"body\""})
		return
	}
	action := req.Action
	// An empty action is the branch at 0x6bdeac. It calls the shared
	// writer with status "ok", error_code 0, and an empty msg, then
	// logs category wireguard, event status_queried, message
	// "Queried WireGuard status".
	if action == "" {
		s.noteEvent("status_queried", "Queried WireGuard status")
		writeJSON(w, wireGuardBody(0, "", false, false))
		return
	}
	// A present manager calls the support method at 0x6be05c before it
	// compares the action. A zero result is the tbz at 0x6be060. The
	// writer at 0x6be9bc stores status "error", code 50021, and the
	// 43-byte msg "wireguard is not supported on this platform". The
	// log at 0x6be970 is level "warn", category "wireguard", event
	// "unsupported", and "WireGuard is not supported on this platform".
	// The Linux manager method returns false.
	if !wireGuardSupported() {
		s.noteEvent("unsupported", "WireGuard is not supported on this platform")
		writeJSON(w, wireGuardBody(50021, "wireguard is not supported on this platform", false, false))
		return
	}
	// The action is compared with the two bytes "on" (0x6e6f) and the
	// three bytes "off" (0x66, 0x6f, 0x66). Any other value is the
	// writer at 0x6be874: error 40004 and the 32-byte msg
	// "action must be empty, on, or off". The log at 0x6be83c is level
	// "warn", category "wireguard", event "invalid_action", and the
	// 33-byte message "Rejected invalid WireGuard action".
	if action != "on" && action != "off" {
		s.noteEvent("invalid_action", "Rejected invalid WireGuard action")
		writeJSON(w, map[string]any{
			"status":     "error",
			"error_code": 40004,
			"msg":        "action must be empty, on, or off",
			"enabled":    false,
			"supported":  false,
			"running":    false,
		})
		return
	}
	// Start looks up wireguard_manager.sh. The lookup miss returns the
	// 34-byte sentence. A found script is run with the command the
	// manager passes: "install" before "start", or "stop". The shipped
	// macOS script is what a production install puts there. Tests point
	// PCSVR_WIREGUARD_SCRIPT at a local script.
	script := wireGuardScript()
	if script == "" {
		s.noteEvent("update_failed", action)
		writeJSON(w, wireGuardBody(50022, "WireGuard manager script not found", false, false))
		return
	}
	if err := runWireGuardScript(r.Context(), script, action); err != nil {
		code := 50022
		msg := "Failed to start WireGuard from API request"
		event := "update_failed"
		data := err.Error()
		if action == "off" {
			code = 50023
			msg = "Failed to stop WireGuard from API request"
		} else if errors.Is(err, errWireGuardInstall) {
			// Manager.Start records "WireGuard installation failed"
			// when Install returns. The value it returns is the
			// 47-byte wrap, which is not the HTTP msg.
			event = "install_failed"
			data = "WireGuard installation failed"
		}
		s.noteEvent(event, data)
		writeJSON(w, wireGuardBody(code, msg, false, false))
		return
	}
	svc := s.service()
	svc.mu.Lock()
	svc.wireguard = action == "on"
	running := svc.wireguard
	svc.mu.Unlock()
	s.noteEvent("updated", action)
	writeJSON(w, wireGuardBody(0, "Updated WireGuard state from API request", true, running))
}

func wireGuardBody(code int, msg string, supported, running bool) map[string]any {
	status := "ok"
	if code != 0 {
		status = "error"
	}
	// UpdateWireGuardResp at file 0x1067450 is 56 bytes and eight
	// fields: status, error_code, enabled, supported, installed,
	// running, wireguard_status, msg. The shared writer at 0x6beab0
	// stores those and does not store an action.
	return map[string]any{
		"status":           status,
		"error_code":       code,
		"enabled":          running,
		"supported":        supported,
		"installed":        false,
		"running":          running,
		"wireguard_status": "",
		"msg":              msg,
	}
}

// wireGuardSupported is the manager method called at 0x6be05c. The
// Apple binary takes the supported path. This port returns false, so
// a present manager reaches the 50021 writer.
func wireGuardSupported() bool {
	return false
}

// errWireGuardInstall marks a failed "install" command. Start logs that
// as "WireGuard installation failed" and returns
// "failed to install WireGuard before starting: %w".
var errWireGuardInstall = errors.New("wireguard install")

// runWireGuardScript runs the manager script the way Start and Stop do.
// "on" is install, then start. "off" is stop. Install's non-zero exit
// is wrapped with the 47-byte sentence. Stop keeps the script text,
// which Stop passes to the stop_failed event.
func runWireGuardScript(ctx context.Context, script, action string) error {
	commands := []string{"stop"}
	if action == "on" {
		commands = []string{"install", "start"}
	}
	for _, command := range commands {
		cmd := exec.CommandContext(ctx, script, command)
		out, err := cmd.CombinedOutput()
		if err != nil {
			text := strings.TrimSpace(string(out))
			if text == "" {
				text = err.Error()
			}
			if command == "install" {
				return fmt.Errorf("failed to install WireGuard before starting: %s: %w", text, errWireGuardInstall)
			}
			return fmt.Errorf("%s", text)
		}
	}
	return nil
}

// wireGuardScript reports the first wireguard_manager.sh on PATH or next
// to the binary. PCSVR_WIREGUARD_SCRIPT overrides that lookup. An empty
// result is the lookup miss Start logs.
func wireGuardScript() string {
	if path := os.Getenv("PCSVR_WIREGUARD_SCRIPT"); path != "" {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return ""
		}
		return path
	}
	if path, err := exec.LookPath("wireguard_manager.sh"); err == nil {
		return path
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(exe), "wireguard_manager.sh")
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return ""
	}
	return candidate
}

func (s *state) listEvents(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	level := r.URL.Query().Get("level")
	name := r.URL.Query().Get("event")
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n := 0
		ok := true
		for _, c := range raw {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			n = n*10 + int(c-'0')
		}
		if ok && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	svc := s.service()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	var events []loggedEvent
	for _, event := range svc.events {
		if category != "" && event.Category != category {
			continue
		}
		if level != "" && event.Level != level {
			continue
		}
		if name != "" && event.Event != name {
			continue
		}
		events = append(events, event)
	}
	if events == nil {
		events = []loggedEvent{}
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	writeJSON(w, map[string]any{"status": "ok", "events": events})
}

func (s *state) listRequests(w http.ResponseWriter, r *http.Request) {
	svc := s.service()
	svc.mu.Lock()
	guard := svc.capture
	svc.mu.Unlock()
	// RequestDebugListHandler writes a map with two keys: the 6-byte
	// word "status" set to "ok", and the 5-byte word "guard" set from
	// the capture flag.
	writeJSON(w, map[string]any{"status": "ok", "guard": guard})
}

func (s *state) captureRequests(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil || body.Enabled == nil {
		writeJSON(w, map[string]any{"status": "error", "error_code": 40007, "msg": "invalid capture toggle payload"})
		return
	}
	svc := s.service()
	svc.mu.Lock()
	svc.capture = *body.Enabled
	enabled := svc.capture
	svc.mu.Unlock()
	// The success writer at 0x6d2478 stores status "ok", error_code 0,
	// and capture_enabled. It does not store msg.
	writeJSON(w, map[string]any{
		"status":          "ok",
		"error_code":      0,
		"capture_enabled": enabled,
	})
}

func (s *state) clearRequests(w http.ResponseWriter, r *http.Request) {
	// The clear handler at 0x6d24f0 writes status "ok", error_code 0,
	// and the 7-byte msg "cleared". The route table registers it as
	// DELETE /debug/requests. The writer does not store the capture flag.
	writeJSON(w, map[string]any{"status": "ok", "error_code": 0, "msg": "cleared"})
}

func (s *state) debugUnlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token     string `json:"token"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
		Chain     string `json:"chain"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if len(strings.TrimSpace(string(raw))) == 0 || json.Unmarshal(raw, &body) != nil || strings.TrimSpace(body.Token) == "" || strings.TrimSpace(body.Nonce) == "" || strings.TrimSpace(body.Signature) == "" {
		s.noteEvent("unlock_denied", "invalid unlock payload")
		writeJSON(w, map[string]any{"status": "error", "error_code": 40010, "msg": "invalid unlock payload"})
		return
	}
	if err := s.authorizeDebug(body.Token, body.Nonce, body.Signature, body.Chain); err != nil {
		s.noteEvent("unlock_denied", err.Error())
		msg := "unlock denied"
		code := 40101
		switch err.Error() {
		case "invalid unlock payload":
			msg = err.Error()
			code = 40010
		case "stale unlock request", "no certificate found", "invalid nonce length":
			msg = err.Error()
			code = 40101
		}
		writeJSON(w, map[string]any{"status": "error", "error_code": code, "msg": msg})
		return
	}
	svc := s.service()
	svc.mu.Lock()
	svc.unlocked = true
	svc.mu.Unlock()
	s.noteEvent("unlock_granted", "debug_auth")
	token, expires, err := debugGrant()
	if err != nil {
		s.noteEvent("unlock_denied", err.Error())
		writeJSON(w, map[string]any{"status": "error", "error_code": 40101, "msg": "unlock denied"})
		return
	}
	// The success writer at 0x6d2170 stores status "ok", error_code 0,
	// debug_token, and expires_at. It leaves msg empty. "Debug unlock
	// granted" is the log line at 0x6a2040.
	writeJSON(w, map[string]any{
		"status": "ok", "error_code": 0,
		"debug_token": token, "expires_at": expires, "msg": "",
	})
}

// debugGrant is the success tail of Authority.Unlock. It reads 32
// random bytes (the io.ReadFull at 0x19c8e0), hex-encodes them with the
// alphabet at 0x746378, and returns time.Now plus 10 seconds as a unix
// timestamp. The 10 seconds are the immediate 0x3b9aca00 added before
// time.Time.Unix at 0xaed80. The HTTP writer stores that string at
// debug_token and that int64 at expires_at.
func debugGrant() (string, int64, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, err
	}
	expires := time.Now().Add(10 * time.Second).Unix()
	return hex.EncodeToString(raw), expires, nil
}

// authorizeDebug is Authority.Unlock. The leaf certificate must verify
// against the embedded Tiiny Dev Center root, its organization must be
// the 16 bytes "tiiny-debug-tool", and the nonce must be 32 bytes that
// have not been seen in the last 300 seconds. The signature is ECDSA
// SHA-256 over the nonce bytes.
func (s *state) authorizeDebug(token, nonceHex, signatureHex, chainPEM string) error {
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil || len(nonce) != 32 {
		return fmt.Errorf("invalid nonce length")
	}
	roots, err := debugRoots()
	if err != nil {
		return fmt.Errorf("debugauth: embedded root CA failed to parse")
	}
	der, err := decodeDebugBlob(token)
	if err != nil {
		return fmt.Errorf("no certificate found")
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("no certificate found")
	}
	var intermediates *x509.CertPool
	if strings.TrimSpace(chainPEM) != "" {
		intermediates = x509.NewCertPool()
		if !intermediates.AppendCertsFromPEM([]byte(chainPEM)) {
			return fmt.Errorf("no certificate found")
		}
	}
	if !debugLeafTrusted(leaf, roots, intermediates) {
		return fmt.Errorf("unlock denied")
	}
	if len(leaf.Subject.Organization) != 1 || leaf.Subject.Organization[0] != "tiiny-debug-tool" {
		return fmt.Errorf("unlock denied")
	}
	sig, err := decodeDebugBlob(signatureHex)
	if err != nil || len(sig) == 0 {
		return fmt.Errorf("unlock denied")
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !verifyECDSA(pub, nonce, sig) {
		return fmt.Errorf("unlock denied")
	}
	svc := s.service()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	now := time.Now()
	for key, seen := range svc.nonces {
		if now.Sub(seen) > debugNonceTTL {
			delete(svc.nonces, key)
		}
	}
	key := hex.EncodeToString(nonce)
	if seen, ok := svc.nonces[key]; ok && now.Sub(seen) <= debugNonceTTL {
		return fmt.Errorf("stale unlock request")
	}
	svc.nonces[key] = now
	return nil
}

func verifyECDSA(pub *ecdsa.PublicKey, msg, sig []byte) bool {
	sum := sha256.Sum256(msg)
	if ecdsa.VerifyASN1(pub, sum[:], sig) {
		return true
	}
	size := (pub.Curve.Params().BitSize + 7) / 8
	if len(sig) != size*2 {
		return false
	}
	r := new(big.Int).SetBytes(sig[:size])
	s := new(big.Int).SetBytes(sig[size:])
	return ecdsa.Verify(pub, sum[:], r, s)
}

func decodeDebugBlob(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if der, err := base64.StdEncoding.DecodeString(text); err == nil && len(der) > 0 {
		return der, nil
	}
	if der, err := base64.RawStdEncoding.DecodeString(text); err == nil && len(der) > 0 {
		return der, nil
	}
	if der, err := hex.DecodeString(text); err == nil && len(der) > 0 {
		return der, nil
	}
	return nil, fmt.Errorf("decode")
}

// debugLeafTrusted is the certificate check inside Authority.Unlock.
// The leaf must verify to the embedded Tiiny Dev Center root. The
// binary does not contain that root's private key, so a test that
// sets the same certificate as the process root (the public half
// already embedded) still has to present a chain that ends there.
func debugLeafTrusted(leaf *x509.Certificate, roots *x509.CertPool, intermediates *x509.CertPool) bool {
	opts := x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: time.Now()}
	chains, err := leaf.Verify(opts)
	if err != nil || len(chains) == 0 || !chainEndsAtEmbeddedRoot(chains) {
		return false
	}
	return true
}

func chainEndsAtEmbeddedRoot(chains [][]*x509.Certificate) bool {
	root, err := embeddedDebugRoot()
	if err != nil {
		return false
	}
	for _, chain := range chains {
		if len(chain) == 0 {
			continue
		}
		last := chain[len(chain)-1]
		if last.Equal(root) {
			return true
		}
	}
	return false
}

func embeddedDebugRoot() (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(debugRootPEM))
	if block == nil {
		return nil, fmt.Errorf("pem")
	}
	return x509.ParseCertificate(block.Bytes)
}

func debugRoots() (*x509.CertPool, error) {
	block, _ := pem.Decode([]byte(debugRootPEM))
	if block == nil {
		return nil, fmt.Errorf("pem")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return pool, nil
}

func (s *state) pprofStatus(w http.ResponseWriter, r *http.Request) {
	// The handler at 0x6d1de0 loads the pprof manager at svc+0x630.
	// A nil manager stores status "error", error_code 40302, and the
	// 17-byte msg "guard unavailable". It does not store enabled.
	// 50012 is a later branch that copies a dynamic message. This
	// process has no manager.
	writeJSON(w, map[string]any{
		"status":     "error",
		"error_code": 40302,
		"msg":        "guard unavailable",
	})
}

// pprofToggle is POST /debug/pprof. PprofToggleHandler compares the body
// with the 4 bytes "ping" and the 9 bytes "heartbeat". Either one turns
// the endpoint on. PprofToggleResp is {"enabled": ...}.
func (s *state) pprofToggle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	action := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	var body struct {
		Action  string `json:"action"`
		Enabled *bool  `json:"enabled"`
	}
	_ = json.Unmarshal(raw, &body)
	if body.Action != "" {
		action = strings.TrimSpace(body.Action)
	}
	if action != "ping" && action != "heartbeat" && body.Enabled == nil {
		writeJSON(w, map[string]any{"status": "error", "error_code": 40011, "msg": "invalid pprof toggle payload"})
		return
	}
	svc := s.service()
	svc.mu.Lock()
	switch action {
	case "ping", "heartbeat":
		svc.pprofOn = true
	default:
		svc.pprofOn = *body.Enabled
	}
	enabled := svc.pprofOn
	svc.mu.Unlock()
	writeJSON(w, map[string]any{"status": "ok", "error_code": 0, "enabled": enabled})
}

// guardUnavailable is the nil-guard path of the handlers at 0x6d19c0
// and 0x6d1b30. The response type at 0x104b9e0 is status, error_code,
// active, enabled, msg. A nil guard at svc+0x638 stores status
// "error", error_code 40302, and the 17-byte msg "guard unavailable".
// It does not store active or enabled. This process has no guard.
func guardUnavailable(w http.ResponseWriter) {
	writeJSON(w, map[string]any{
		"status": "error", "error_code": 40302, "msg": "guard unavailable",
	})
}

func (s *state) guardStatus(w http.ResponseWriter, r *http.Request) {
	guardUnavailable(w)
}

func (s *state) guardStandDown(w http.ResponseWriter, r *http.Request) {
	guardUnavailable(w)
}

func (s *state) swagger(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"swagger": "2.0",
		"info":    map[string]string{"title": "pcsvr", "version": versionString()},
		"paths": map[string]any{
			"/version":                            map[string]any{"get": map[string]any{}},
			"/status":                             map[string]any{"get": map[string]any{}},
			"/devices_list":                       map[string]any{"get": map[string]any{}},
			"/devices_list/ws":                    map[string]any{"get": map[string]any{}},
			"/select_device":                      map[string]any{"post": map[string]any{}},
			"/account/update_auth_key":            map[string]any{"post": map[string]any{}},
			"/account/get_temporary_auth_key":     map[string]any{"post": map[string]any{}},
			"/account/list_auth_keys":             map[string]any{"get": map[string]any{}},
			"/account/list_temporary_auth_keys":   map[string]any{"get": map[string]any{}},
			"/account/delete_auth_key":            map[string]any{"post": map[string]any{}},
			"/account/delete_temporary_auth_keys": map[string]any{"post": map[string]any{}},
			"/account/update_usb_auto_eject":      map[string]any{"post": map[string]any{}},
			"/account/update_wireguard":           map[string]any{"post": map[string]any{}},
			"/dns/records":                        map[string]any{"get": map[string]any{}},
			"/dns/resolve":                        map[string]any{"post": map[string]any{}},
			"/dns/update":                         map[string]any{"post": map[string]any{}},
			"/usb-storage":                        map[string]any{"get": map[string]any{}, "post": map[string]any{}},
		},
	})
}

func (s *state) usbStorage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Active *int `json:"active"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body)
	blocked := body.Active != nil && *body.Active == 0
	svc := s.service()
	svc.mu.Lock()
	svc.usbBlock = blocked
	svc.mu.Unlock()
	writeJSON(w, usbStorageBody(blocked, ""))
}

func (s *state) usbStorageStatus(w http.ResponseWriter, r *http.Request) {
	svc := s.service()
	svc.mu.Lock()
	blocked := svc.usbBlock
	svc.mu.Unlock()
	writeJSON(w, usbStorageBody(blocked, ""))
}

// usbStorageBody is the box reply: ok, blocked, and error. An empty
// error is omitted.
func usbStorageBody(blocked bool, errText string) map[string]any {
	body := map[string]any{"ok": true, "blocked": blocked}
	if errText != "" {
		body["ok"] = false
		body["error"] = errText
	}
	return body
}

func (s *state) webUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "<!doctype html><title>pcsvr</title><p>api port 60000</p><p>web ui port 60001</p>")
}

func (s *state) rememberAuth(deviceID, authKey string) {
	if deviceID == "" {
		return
	}
	svc := s.service()
	svc.mu.Lock()
	prev := svc.keys[deviceID]
	prev.DeviceID = deviceID
	prev.AuthKey = authKey
	prev.Updated = time.Now().UTC()
	svc.keys[deviceID] = prev
	svc.mu.Unlock()
}

// tempKeyData is the string the lookup returns and the handler stores.
// The format is the 14 bytes "%s|%s|%d|%s|%s", then the 4-byte prefix
// "tak|". The HMAC is SHA-256 of the 8-byte big-endian counter
// floor(unix/300). The key is the stored auth key, at most 32 bytes.
// The code is the dynamic truncation modulo 1000000.
//
// The five interface values stored at 0x6baf6c are:
//
//	sp+0xc8  first five HMAC bytes
//	sp+0xd8  sprintf("%d") of the unix seconds at [sp,#0x48]
//	sp+0xe8  the counter at [sp,#0x58], which is the %d
//	sp+0xf8  sprintf("%06d") of the code at [sp,#0x70]
//	sp+0x108 sprintf("%x") of the 4-byte copy of the digest prefix
func tempKeyData(authKey string, now time.Time) string {
	key := []byte(authKey)
	if len(key) > 32 {
		key = key[:32]
	}
	unix := now.Unix()
	step := unix / 300
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(counter[:])
	sum := mac.Sum(nil)
	off := int(sum[len(sum)-1] & 0x0f)
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	code := int(bin % 1000000)
	prefix := sum
	if len(prefix) > 4 {
		prefix = prefix[:4]
	}
	return "tak|" + fmt.Sprintf("%s|%d|%d|%06d|%x", string(sum[:5]), unix, step, code, prefix)
}
