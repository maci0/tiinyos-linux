package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var maskKey = [4]byte{0x11, 0x22, 0x33, 0x44}

func maskWS(payload []byte, key [4]byte) []byte {
	out := make([]byte, len(payload))
	for i, b := range payload {
		out[i] = b ^ key[i%4]
	}
	return out
}

type wsConn struct {
	net.Conn
	pending []byte
}

func (c *wsConn) Read(p []byte) (int, error) {
	if len(c.pending) == 0 {
		return c.Conn.Read(p)
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	if n == len(p) || len(c.pending) > 0 {
		return n, nil
	}
	m, err := c.Conn.Read(p[n:])
	return n + m, err
}

func dialWS(t *testing.T, srv *httptest.Server, path string) net.Conn {
	t.Helper()
	raw, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	req := "GET " + path + " HTTP/1.1\r\nHost: " + srv.Listener.Addr().String() + "\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
	if _, err = raw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8192)
	got := 0
	split := -1
	for split < 0 {
		n, err := raw.Read(buf[got:])
		if err != nil {
			t.Fatal(err)
		}
		got += n
		split = strings.Index(string(buf[:got]), "\r\n\r\n")
	}
	if !strings.Contains(string(buf[:split]), "101 ") {
		t.Fatalf("handshake %s", buf[:split])
	}
	return &wsConn{Conn: raw, pending: append([]byte(nil), buf[split+4:got]...)}
}

func readWSJSON(t *testing.T, conn net.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatal(err)
	}
	size := int(hdr[1] & 0x7f)
	if size == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(conn, ext); err != nil {
			t.Fatal(err)
		}
		size = int(ext[0])<<8 | int(ext[1])
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(buf, &event); err != nil {
		t.Fatalf("ws payload size %d %v %q", size, err, buf)
	}
	return event
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := newState(config{DeviceTTL: defaultDeviceTTL, Domain: defaultDomain, DNS: true, SSH: true})
	mux := http.NewServeMux()
	register(mux, s)
	return httptest.NewServer(mux)
}

func call(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil && len(raw) > 0 && raw[0] == '{' {
		t.Fatalf("%s %s: %v %s", method, path, err, raw)
	}
	return resp.StatusCode, out
}

func writeWireGuardScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/wireguard_manager.sh"
	body := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$0.log\"\nexit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFailingWireGuardScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/wireguard_manager.sh"
	body := "#!/bin/sh\necho fail\nexit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func debugUnlockBody(t *testing.T, org string) string {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Tiiny Dev Center"}, CommonName: "Tiiny Dev Center"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{Organization: []string{org}, CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(nonce)
	sig, err := ecdsa.SignASN1(rand.Reader, leafKey, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{
		"token":     base64.StdEncoding.EncodeToString(leafDER),
		"nonce":     hex.EncodeToString(nonce),
		"signature": base64.StdEncoding.EncodeToString(sig),
		"chain":     string(caPEM),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestServiceRoutesThroughServer(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	code, body := call(t, srv, http.MethodPost, "/account/update_auth_key", `{`)
	if code != 200 || body["error_code"] != float64(40001) || body["msg"] != "type mismatch for field \"body\"" {
		t.Fatalf("bad json: %d %+v", code, body)
	}
	code, body = call(t, srv, http.MethodPost, "/account/update_auth_key", ``)
	if code != 200 || body["msg"] != "Rejected auth key update because request is empty" || body["error_code"] != float64(40001) {
		t.Fatalf("empty body: %d %+v", code, body)
	}
	code, body = call(t, srv, http.MethodPost, "/account/update_auth_key", `{"device_id":"","auth_key":"x"}`)
	if body["msg"] != "Rejected auth key update because device_id or auth_key is missing" || body["error_code"] != float64(40001) {
		t.Fatalf("missing id: %d %+v", code, body)
	}
	code, body = call(t, srv, http.MethodPost, "/account/update_auth_key", `{"device_id":"TNYM","auth_key":"short"}`)
	if body["msg"] != "Rejected auth key update because the auth key is too short" {
		t.Fatalf("short key: %d %+v", code, body)
	}
	if authKeyPresent("") {
		t.Fatal("empty auth key is present")
	}
	if !authKeyPresent("0123456789abcdef0123456789abcdef") {
		t.Fatal("stored auth key is absent")
	}
	authDir := t.TempDir()
	t.Setenv("PCSVR_AUTH_DIR", authDir)
	code, body = call(t, srv, http.MethodPost, "/account/update_auth_key", `{"device_id":"TNYM","auth_key":"0123456789abcdef0123456789abcdef"}`)
	if body["status"] != "ok" || body["msg"] != "Updated auth key for device" {
		t.Fatalf("update: %d %+v", code, body)
	}
	_, updateLog := call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=auth_key_updated&limit=5", "")
	updated, _ := updateLog["events"].([]any)
	if len(updated) == 0 {
		t.Fatalf("update log %+v", updateLog)
	}
	updatedEvent, _ := updated[0].(map[string]any)
	if updatedEvent["level"] != "info" || updatedEvent["category"] != "auth" || updatedEvent["event"] != "auth_key_updated" || updatedEvent["message"] != "Updated auth key for device" {
		t.Fatalf("update data %+v", updatedEvent)
	}
	_, mismatchLog := call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=storage_inconsistency&limit=5", "")
	if mismatch, _ := mismatchLog["events"].([]any); len(mismatch) != 0 {
		t.Fatalf("matching save logged inconsistency %+v", mismatchLog)
	}
	saved, err := os.ReadFile(filepath.Join(authDir, "TNYM.json"))
	if err != nil {
		t.Fatal(err)
	}
	var savedBody map[string]string
	if err = json.Unmarshal(saved, &savedBody); err != nil {
		t.Fatal(err)
	}
	if savedBody["auth_key"] != "0123456789abcdef0123456789abcdef" || savedBody["expire_time"] == "" {
		t.Fatalf("auth file %+v", savedBody)
	}
	_, body = call(t, srv, http.MethodGet, "/account/auth_keys", "")
	keys, _ := body["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("keys %+v", body)
	}
	listed, _ := keys[0].(map[string]any)
	if listed["device_id"] != "TNYM" || listed["key_preview"] != "0123456789abcdef0123456789ab****" || listed["expired"] != false || listed["expire_time"] == "" {
		t.Fatalf("summary %+v", listed)
	}
	if authKeyPreview("short") != "****" || authKeyPreview("0123456789abcdef0123456789abcdef") != "0123456789abcdef0123456789ab****" {
		t.Fatal("preview")
	}
	past := time.Now().UTC().Add(-time.Minute)
	savedBody["expire_time"] = past.Format(time.RFC3339Nano)
	rewritten, err := json.Marshal(savedBody)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(authDir, "TNYM.json"), rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	_, body = call(t, srv, http.MethodPost, "/account/get_temporary_auth_key", `{"agent_id":"agent456","device_id":"TNYM"}`)
	if body["msg"] != "Auth key expired for device: TNYM" || body["error_code"] != float64(50001) {
		t.Fatalf("expired %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=temporary_auth_key_failed&limit=5", "")
	expiredLog, _ := body["events"].([]any)
	if len(expiredLog) == 0 {
		t.Fatalf("expired log %+v", body)
	}
	expiredEvent, _ := expiredLog[0].(map[string]any)
	if expiredEvent["message"] != "Temporary auth key request failed because auth key expired" {
		t.Fatalf("expired data %+v", expiredEvent)
	}
	future := time.Now().UTC().Add(time.Hour)
	savedBody["expire_time"] = future.Format(time.RFC3339Nano)
	rewritten, err = json.Marshal(savedBody)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(authDir, "TNYM.json"), rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	verifyDir := t.TempDir()
	if err = os.Symlink("/dev/null", filepath.Join(verifyDir, "TNYM.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PCSVR_AUTH_DIR", verifyDir)
	_, body = call(t, srv, http.MethodPost, "/account/update_auth_key", `{"device_id":"TNYM","auth_key":"0123456789abcdef0123456789abcdef"}`)
	if body["status"] != "error" || body["error_code"] != float64(50001) || body["msg"] != "Failed to verify auth key persistence" {
		t.Fatalf("verify %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=update_auth_key_failed&limit=5", "")
	verifyEvents, _ := body["events"].([]any)
	var verifyEvent map[string]any
	for _, item := range verifyEvents {
		event, _ := item.(map[string]any)
		if event["message"] == "Failed to verify persisted auth key" {
			verifyEvent = event
			break
		}
	}
	if verifyEvent == nil || verifyEvent["level"] != "error" || verifyEvent["category"] != "auth" || verifyEvent["event"] != "update_auth_key_failed" {
		t.Fatalf("verify log %+v", body)
	}
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, "not-a-directory"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PCSVR_AUTH_DIR", filepath.Join(blocked, "not-a-directory", "auth_data"))
	_, body = call(t, srv, http.MethodPost, "/account/update_auth_key", `{"device_id":"TNYM","auth_key":"0123456789abcdef0123456789abcdef"}`)
	if body["msg"] != "Failed to save auth key to persistent storage" || body["error_code"] != float64(50001) {
		t.Fatalf("persist %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=update_auth_key_failed&limit=5", "")
	failed, _ := body["events"].([]any)
	var loggedFail map[string]any
	for _, item := range failed {
		event, _ := item.(map[string]any)
		if event["message"] == "Failed to persist auth key" {
			loggedFail = event
			break
		}
	}
	if loggedFail == nil || loggedFail["level"] != "error" || loggedFail["category"] != "auth" || loggedFail["event"] != "update_auth_key_failed" {
		t.Fatalf("persist data %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/account/get_temporary_auth_key", `{}`)
	if body["msg"] != "Rejected temporary auth key request: empty request" || body["error_code"] != float64(41001) {
		t.Fatalf("empty request %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/account/get_temporary_auth_key", `{"device_id":"TNYM"}`)
	if body["msg"] != "Rejected temporary auth key request because agent_id or device_id is missing" || body["error_code"] != float64(41001) {
		t.Fatalf("missing agent %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/account/get_temporary_auth_key", `{"agent_id":"agent456","device_id":"missing"}`)
	if body["msg"] != "PC Service don't have auth key for device: missing" || body["error_code"] != float64(50001) {
		t.Fatalf("unknown device %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=temporary_auth_key_failed&limit=5", "")
	missingLog, _ := body["events"].([]any)
	foundMissing := false
	for _, item := range missingLog {
		event, _ := item.(map[string]any)
		if event["message"] == "Temporary auth key request failed because auth key is missing" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("missing data %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/account/get_temporary_auth_key", `{"agent_id":"agent456","device_id":"TNYM"}`)
	token, _ := body["data"].(string)
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["msg"] != "" || body["expires_at"] != nil || token == "" {
		t.Fatalf("token %+v", body)
	}
	// The five fields are computed here, not by calling tempKeyData.
	// Order from the stores at 0x6baf6c: five HMAC bytes, "%d" of the
	// unix seconds, the counter at [sp,#0x58], "%06d" of the code, and
	// "%x" of the first four digest bytes. The prefix is "tak|".
	// The handler and this check call time.Now separately, so the unix
	// second in slot 2 can fall on either side of the boundary.
	var matched string
	for _, unix := range []int64{time.Now().Unix(), time.Now().Unix() - 1} {
		var counter [8]byte
		step := unix / 300
		binary.BigEndian.PutUint64(counter[:], uint64(step))
		mac := hmac.New(sha256.New, []byte("0123456789abcdef0123456789abcdef"))
		_, _ = mac.Write(counter[:])
		sum := mac.Sum(nil)
		off := int(sum[len(sum)-1] & 0x0f)
		bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
		otp := int(bin % 1000000)
		want := "tak|" + string(sum[:5]) + "|" + fmt.Sprintf("%d", unix) + "|" + fmt.Sprintf("%d", step) + "|" + fmt.Sprintf("%06d", otp) + "|" + fmt.Sprintf("%x", sum[:4])
		round, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var decoded string
		if err = json.Unmarshal(round, &decoded); err != nil {
			t.Fatal(err)
		}
		if token == decoded {
			matched = decoded
			break
		}
	}
	if matched == "" {
		t.Fatalf("data %q", token)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=temporary_auth_key_generated&limit=5", "")
	generated, _ := body["events"].([]any)
	if len(generated) == 0 {
		t.Fatalf("temp key log %+v", body)
	}
	logged, _ := generated[0].(map[string]any)
	if logged["message"] != "Generated temporary auth key for agent: agent456, device: TNYM" {
		t.Fatalf("temp key data %+v", logged)
	}
	_, body = call(t, srv, http.MethodGet, "/account/temporary_auth_keys", "")
	temps, _ := body["keys"].([]any)
	if len(temps) != 1 {
		t.Fatalf("temps %+v", body)
	}
	tempSummary, _ := temps[0].(map[string]any)
	wantPreview := authKeyPreview(token)
	round, err := json.Marshal(wantPreview)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(round, &wantPreview); err != nil {
		t.Fatal(err)
	}
	if tempSummary["device_id"] != "TNYM" || tempSummary["key_preview"] != wantPreview || tempSummary["expired"] != false || tempSummary["expire_time"] == "" {
		t.Fatalf("temp summary %+v", tempSummary)
	}
	_, body = call(t, srv, http.MethodDelete, "/account/temporary_auth_keys", `{}`)
	if body["msg"] != "device_id is required" || body["error_code"] != float64(40001) {
		t.Fatalf("delete temp empty fields %+v", body)
	}
	_, body = call(t, srv, http.MethodDelete, "/account/temporary_auth_keys", ``)
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["removed"] != float64(0) || body["msg"] != "" {
		t.Fatalf("delete temp empty body %+v", body)
	}
	_, body = call(t, srv, http.MethodDelete, "/account/temporary_auth_keys", `{"device_id":"TNYM"}`)
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["removed"] != float64(1) || body["msg"] != "" {
		t.Fatalf("delete temp %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=temporary_auth_keys_deleted&limit=5", "")
	tempDeleted, _ := body["events"].([]any)
	if len(tempDeleted) == 0 {
		t.Fatalf("temp delete log %+v", body)
	}
	tempEvent, _ := tempDeleted[0].(map[string]any)
	if tempEvent["level"] != "info" || tempEvent["category"] != "auth" || tempEvent["event"] != "temporary_auth_keys_deleted" || tempEvent["message"] != "Temporary auth keys deleted" {
		t.Fatalf("temp delete data %+v", tempEvent)
	}
	_, body = call(t, srv, http.MethodGet, "/account/temporary_auth_keys", "")
	if keys, _ := body["keys"].([]any); len(keys) != 0 {
		t.Fatalf("temp remains %+v", body)
	}
	_, body = call(t, srv, http.MethodDelete, "/account/auth_keys", `{"device_id":""}`)
	if body["msg"] != "device_id is required" || body["error_code"] != float64(40001) {
		t.Fatalf("delete empty %+v", body)
	}
	_, body = call(t, srv, http.MethodDelete, "/account/auth_keys", `{"device_id":"missing"}`)
	if body["msg"] != "failed to remove auth file for device missing" || body["error_code"] != float64(50011) {
		t.Fatalf("delete missing %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=delete_auth_key_failed&limit=5", "")
	deleteFailed, _ := body["events"].([]any)
	if len(deleteFailed) == 0 {
		t.Fatalf("delete log %+v", body)
	}
	deleteEvent, _ := deleteFailed[0].(map[string]any)
	if deleteEvent["level"] != "error" || deleteEvent["category"] != "auth" || deleteEvent["event"] != "delete_auth_key_failed" || deleteEvent["message"] != "Failed to delete auth key" {
		t.Fatalf("delete data %+v", deleteEvent)
	}
	t.Setenv("PCSVR_AUTH_DIR", authDir)
	os.Remove(filepath.Join(authDir, "FILE.json"))
	fileKey := []byte(`{"auth_key":"0123456789abcdef0123456789abcdef","expire_time":"` + time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano) + `"}`)
	if err = os.WriteFile(filepath.Join(authDir, "FILE.json"), fileKey, 0o600); err != nil {
		t.Fatal(err)
	}
	_, body = call(t, srv, http.MethodPost, "/pcsvr/account/get_temporary_auth_key", `{"agent_id":"agent456","device_id":"FILE"}`)
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["msg"] != "" || body["data"] == "" {
		t.Fatalf("file key %+v", body)
	}
	_, body = call(t, srv, http.MethodDelete, "/account/auth_keys", `{"device_id":"TNYM"}`)
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["msg"] != "" {
		t.Fatalf("delete %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=auth&event=auth_key_deleted&limit=5", "")
	deleted, _ := body["events"].([]any)
	if len(deleted) == 0 {
		t.Fatalf("deleted log %+v", body)
	}
	deletedEvent, _ := deleted[0].(map[string]any)
	if deletedEvent["level"] != "info" || deletedEvent["category"] != "auth" || deletedEvent["event"] != "auth_key_deleted" || deletedEvent["message"] != "Auth key deleted" {
		t.Fatalf("deleted data %+v", deletedEvent)
	}
	if _, err = os.Stat(filepath.Join(authDir, "TNYM.json")); !os.IsNotExist(err) {
		t.Fatalf("auth file remains: %v", err)
	}

	_, body = call(t, srv, http.MethodPost, "/account/update_usb_auto_eject", `{"enabled":true}`)
	if body["status"] != "error" || body["error_code"] != float64(50010) || body["enabled"] != false || body["supported"] != false || body["msg"] != "usb auto eject monitor is unavailable" || body["action"] != nil {
		t.Fatalf("usb %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=usb&event=auto_eject_unavailable&limit=5", "")
	events, _ := body["events"].([]any)
	if len(events) == 0 || events[0].(map[string]any)["message"] != "USB auto eject monitor is unavailable" || events[0].(map[string]any)["category"] != "usb" {
		t.Fatalf("usb log %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=usb&limit=5", "")
	events, _ = body["events"].([]any)
	if len(events) == 0 {
		t.Fatalf("log %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=usb&event=missing&limit=5", "")
	if len(body["events"].([]any)) != 0 {
		t.Fatalf("event filter %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/account/update_wireguard", `{}`)
	if body["status"] != "error" || body["error_code"] != float64(50020) || body["msg"] != "wireguard manager is unavailable" || body["enabled"] != false || body["wireguard_status"] != "Unavailable" || body["action"] != nil {
		t.Fatalf("wg empty nil manager %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/account/update_wireguard", `{"action":"sideways"}`)
	if body["status"] != "error" || body["msg"] != "wireguard manager is unavailable" || body["error_code"] != float64(50020) || body["enabled"] != false || body["wireguard_status"] != "Unavailable" {
		t.Fatalf("wg action %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=wireguard&event=manager_unavailable&limit=5", "")
	invalid, _ := body["events"].([]any)
	if len(invalid) == 0 {
		t.Fatalf("wg action log %+v", body)
	}
	invalidEvent, _ := invalid[0].(map[string]any)
	if invalidEvent["level"] != "warn" || invalidEvent["category"] != "wireguard" || invalidEvent["event"] != "manager_unavailable" || invalidEvent["message"] != "WireGuard manager is unavailable" {
		t.Fatalf("wg action data %+v", invalidEvent)
	}
	// A missing manager is error 50020, wireguard_status Unavailable,
	// and msg "wireguard manager is unavailable". enabled is false.
	// The request type has no enabled field, so that JSON is ignored.
	_, body = call(t, srv, http.MethodPost, "/account/update_wireguard", `{"enabled":true}`)
	if body["msg"] != "wireguard manager is unavailable" || body["error_code"] != float64(50020) || body["enabled"] != false || body["wireguard_status"] != "Unavailable" || body["status"] != "error" {
		t.Fatalf("wg manager %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=wireguard&event=manager_unavailable&limit=5", "")
	events = body["events"].([]any)
	if len(events) == 0 {
		t.Fatalf("wg manager log %+v", body)
	}
	logged, _ = events[0].(map[string]any)
	if logged["level"] != "warn" || logged["message"] != "WireGuard manager is unavailable" || logged["category"] != "wireguard" {
		t.Fatalf("wg manager event %+v", logged)
	}
	_, body = call(t, srv, http.MethodPost, "/account/update_wireguard", `{"action":"off"}`)
	if body["msg"] != "wireguard manager is unavailable" || body["error_code"] != float64(50020) || body["enabled"] != false {
		t.Fatalf("wg off %+v", body)
	}
	script := writeWireGuardScript(t)
	t.Setenv("PCSVR_WIREGUARD_SCRIPT", script)
	_, body = call(t, srv, http.MethodPost, "/account/update_wireguard", `{"action":"on"}`)
	if body["status"] != "error" || body["error_code"] != float64(50021) || body["msg"] != "wireguard is not supported on this platform" || body["enabled"] != false || body["supported"] != false || body["running"] != false || body["installed"] != false || body["action"] != nil {
		t.Fatalf("wg unsupported %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/pcsvr/log?category=wireguard&event=unsupported&limit=5", "")
	unsupported, _ := body["events"].([]any)
	if len(unsupported) == 0 {
		t.Fatalf("wg unsupported log %+v", body)
	}
	unsupportedEvent, _ := unsupported[0].(map[string]any)
	if unsupportedEvent["level"] != "warn" || unsupportedEvent["category"] != "wireguard" || unsupportedEvent["event"] != "unsupported" || unsupportedEvent["message"] != "WireGuard is not supported on this platform" {
		t.Fatalf("wg unsupported data %+v", unsupportedEvent)
	}
	if _, err := os.Stat(script + ".log"); !os.IsNotExist(err) {
		t.Fatalf("unsupported path ran the script: %v", err)
	}
	for _, payload := range []string{`{"action":"sideways"}`, `{"action":"on"}`, `{"action":"off"}`, `{}`} {
		_, body = call(t, srv, http.MethodPost, "/account/update_usb_auto_eject", payload)
		if body["status"] != "error" || body["error_code"] != float64(50010) || body["enabled"] != false || body["supported"] != false || body["msg"] != "usb auto eject monitor is unavailable" || body["action"] != nil {
			t.Fatalf("usb %s %+v", payload, body)
		}
	}
	t.Setenv("PCSVR_WIREGUARD_SCRIPT", "")
	_, body = call(t, srv, http.MethodGet, "/status", "")
	usb, _ := body["usb_auto_eject"].(map[string]any)
	wg, _ := body["wireguard"].(map[string]any)
	dnsBody, _ := body["dns"].(map[string]any)
	version, _ := body["version"].(map[string]any)
	if usb["enabled"] != false || usb["supported"] != false || wg["status"] != "error" || wg["error_code"] != float64(50020) || wg["supported"] != false || wg["enabled"] != false || wg["wireguard_status"] != "Unavailable" || wg["msg"] != "wireguard manager is unavailable" {
		t.Fatalf("status %+v", body)
	}
	sshBody, _ := body["ssh_proxy"].(map[string]any)
	if body["service_status"] != "running" || body["tracked_auth_devices"] != float64(1) || dnsBody["default_ipv4"] != "127.0.0.1" || version["platform"] != "linux/amd64" {
		t.Fatalf("status fields %+v", body)
	}
	if sshBody["status"] != "ok" || sshBody["error_code"] != float64(0) || sshBody["enabled"] != true || sshBody["running"] != true || sshBody["listen_address"] != "127.0.0.1:60022" || sshBody["target_port"] != float64(22) || sshBody["msg"] != "" {
		t.Fatalf("ssh snapshot %+v", sshBody)
	}
	_, pcsvrStatus := call(t, srv, http.MethodGet, "/pcsvr/status", "")
	if pcsvrStatus["service_status"] != "running" || pcsvrStatus["tracked_auth_devices"] != body["tracked_auth_devices"] {
		t.Fatalf("pcsvr status %+v", pcsvrStatus)
	}

	_, body = call(t, srv, http.MethodPost, "/dns/update", `{`)
	if body["error_code"] != float64(40011) || body["msg"] != "type mismatch for field \"body\"" {
		t.Fatalf("dns json %+v", body)
	}
	_, body = call(t, srv, http.MethodDelete, "/account/temporary_auth_keys", `{`)
	if body["error_code"] != float64(40001) || body["msg"] != "type mismatch for field \"body\"" {
		t.Fatalf("temp json %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/update", `{"action":"nope","fqdn":"extra.tiiny.local","ipv4":"10.1.1.1"}`)
	if body["error_code"] != float64(40012) || body["msg"] != "action must be set or delete" {
		t.Fatalf("dns action %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/update", `{"action":"set"}`)
	if body["error_code"] != float64(40010) || body["msg"] != "host is required" {
		t.Fatalf("dns missing host %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/update", `{"action":"set","fqdn":"extra.tiiny.local"}`)
	if body["error_code"] != float64(40011) || body["msg"] != "value is required for set action" {
		t.Fatalf("dns missing ip %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/update", `{"action":"delete","fqdn":"missing.tiiny.local"}`)
	if body["msg"] != "Delete DNS record skipped: host not present" {
		t.Fatalf("dns noop %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/update", `{"fqdn":"extra.tiiny.local","ipv4":"10.1.1.1"}`)
	if body["status"] != "ok" {
		t.Fatalf("dns update %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/update", `{"action":"delete","fqdn":"extra.tiiny.local"}`)
	if body["status"] != "ok" || body["msg"] != "Deleted DNS record" {
		t.Fatalf("dns delete %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/update_record", `{"fqdn":"extra.tiiny.local","ipv4":"10.1.1.1"}`)
	restored, _ := body["record"].(map[string]any)
	if body["status"] != "ok" || body["msg"] != "Updated DNS record" || body["action"] != "set" || restored["fqdn"] != "extra.tiiny.local" || body["record_count"] == float64(0) {
		t.Fatalf("dns restore %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/resolve", `{}`)
	if body["msg"] != "hosts are required" || body["error_code"] != float64(40020) {
		t.Fatalf("resolve empty %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/resolve", `{"hosts":["extra.tiiny.local"]}`)
	recs, _ := body["records"].([]any)
	if len(recs) != 1 || recs[0].(map[string]any)["ipv4"] != "10.1.1.1" {
		t.Fatalf("resolve %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/select_device", `{`)
	if body["error_code"] != float64(40001) || body["msg"] != "type mismatch for field \"body\"" {
		t.Fatalf("select json %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/select_device", `{"device_id":"missing-device"}`)
	if body["error_code"] != float64(40440) || body["msg"] != "local device not found for device_id: missing-device" {
		t.Fatalf("select missing %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/select_device", `{"device_id":""}`)
	if body["status"] != "ok" || body["msg"] != "cleared selected device and restored default page" {
		t.Fatalf("select clear %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/dns/records", "")
	records, _ := body["records"].([]any)
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["domain"] != "tiiny.local" || body["default_ipv4"] != "127.0.0.1" || body["record_count"] != float64(len(records)) || body["msg"] != "" || len(records) < 2 {
		t.Fatalf("records %+v", body)
	}

	_, body = call(t, srv, http.MethodPost, "/debug/requests/capture", `{}`)
	if body["msg"] != "invalid capture toggle payload" || body["error_code"] != float64(40007) {
		t.Fatalf("capture %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/requests/capture", `{"enabled":true}`)
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["capture_enabled"] != true || body["msg"] != nil || body["active"] != nil {
		t.Fatalf("capture on %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/requests/capture", `{"enabled":false}`)
	if body["status"] != "ok" || body["capture_enabled"] != false || body["msg"] != nil {
		t.Fatalf("capture off %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/requests/capture", `{"enabled":true}`)
	_, body = call(t, srv, http.MethodGet, "/debug/requests", "")
	if body["status"] != "ok" || body["guard"] != true || len(body) != 2 {
		t.Fatalf("requests on %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/requests/capture", `{"enabled":false}`)
	_, body = call(t, srv, http.MethodGet, "/debug/requests", "")
	if body["status"] != "ok" || body["guard"] != false {
		t.Fatalf("requests off %+v", body)
	}
	_, body = call(t, srv, http.MethodDelete, "/debug/requests", "")
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["msg"] != "cleared" {
		t.Fatalf("clear %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/unlock", ``)
	if body["msg"] != "invalid unlock payload" || body["error_code"] != float64(40010) {
		t.Fatalf("unlock empty %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/unlock", `{"token":"eA==","nonce":"`+strings.Repeat("ab", 32)+`","signature":"Yg=="}`)
	if body["msg"] != "no certificate found" || body["error_code"] != float64(40101) {
		t.Fatalf("unlock bad cert %+v", body)
	}
	// A leaf signed by a CA that is not the embedded Tiiny Dev Center
	// root is denied even when the organization matches.
	foreign := debugUnlockBody(t, "tiiny-debug-tool")
	_, body = call(t, srv, http.MethodPost, "/debug/unlock", foreign)
	if body["msg"] != "unlock denied" || body["error_code"] != float64(40101) {
		t.Fatalf("unlock foreign %+v", body)
	}
	short := debugUnlockBody(t, "tiiny-debug-tool")
	var parsed map[string]string
	if err := json.Unmarshal([]byte(short), &parsed); err != nil {
		t.Fatal(err)
	}
	parsed["nonce"] = "abcd"
	raw, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/unlock", string(raw))
	if body["msg"] != "invalid nonce length" || body["error_code"] != float64(40101) {
		t.Fatalf("unlock nonce %+v", body)
	}
	if _, ok := body["debug_token"]; ok || body["expires_at"] != nil {
		t.Fatalf("denial carried a grant %+v", body)
	}
	savedRoot := debugRootPEM
	t.Cleanup(func() { debugRootPEM = savedRoot })
	grantCA, grantErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	grantTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(9),
		Subject:               pkix.Name{Organization: []string{"Tiiny Dev Center"}, CommonName: "Tiiny Dev Center"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	grantDER, grantErr := x509.CreateCertificate(rand.Reader, grantTpl, grantTpl, &grantCA.PublicKey, grantCA)
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	debugRootPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: grantDER}))
	grantCert, grantErr := x509.ParseCertificate(grantDER)
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	grantLeafKey, grantErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	grantLeafTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{Organization: []string{"tiiny-debug-tool"}, CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	grantLeafDER, grantErr := x509.CreateCertificate(rand.Reader, grantLeafTpl, grantCert, &grantLeafKey.PublicKey, grantCA)
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	grantNonce := make([]byte, 32)
	if _, grantErr = rand.Read(grantNonce); grantErr != nil {
		t.Fatal(grantErr)
	}
	grantSum := sha256.Sum256(grantNonce)
	grantSig, grantErr := ecdsa.SignASN1(rand.Reader, grantLeafKey, grantSum[:])
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	grantPayload, grantErr := json.Marshal(map[string]string{
		"token":     base64.StdEncoding.EncodeToString(grantLeafDER),
		"nonce":     hex.EncodeToString(grantNonce),
		"signature": base64.StdEncoding.EncodeToString(grantSig),
	})
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	grantBefore := time.Now().Add(10 * time.Second).Unix()
	_, body = call(t, srv, http.MethodPost, "/debug/unlock", string(grantPayload))
	grantAfter := time.Now().Add(10 * time.Second).Unix()
	debugRootPEM = savedRoot
	grantToken, _ := body["debug_token"].(string)
	grantExpires, _ := body["expires_at"].(float64)
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["msg"] != "" || len(grantToken) != 64 {
		t.Fatalf("unlock grant %+v", body)
	}
	if _, grantErr = hex.DecodeString(grantToken); grantErr != nil || int64(grantExpires) < grantBefore || int64(grantExpires) > grantAfter {
		t.Fatalf("grant fields %q %v", grantToken, grantExpires)
	}
	_, body = call(t, srv, http.MethodGet, "/debug/pprof", "")
	if body["status"] != "error" || body["error_code"] != float64(40302) || body["msg"] != "guard unavailable" || body["enabled"] != nil {
		t.Fatalf("pprof off %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/pprof", `{"action":"sideways"}`)
	if body["msg"] != "invalid pprof toggle payload" || body["error_code"] != float64(40011) {
		t.Fatalf("pprof bad %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/debug/pprof", `"heartbeat"`)
	if body["enabled"] != true {
		t.Fatalf("pprof on %+v", body)
	}
	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/debug/guard/status"},
		{http.MethodGet, "/debug/guard/probe"},
		{http.MethodPost, "/debug/guard/standdown"},
	} {
		_, body = call(t, srv, probe.method, probe.path, "")
		if body["status"] != "error" || body["error_code"] != float64(40302) || body["msg"] != "guard unavailable" || body["active"] != nil || body["enabled"] != nil {
			t.Fatalf("guard %s %s %+v", probe.method, probe.path, body)
		}
	}
	_, body = call(t, srv, http.MethodPost, "/usb-storage", `{"active":0}`)
	if body["ok"] != true || body["blocked"] != true || body["error"] != nil {
		t.Fatalf("usb-storage %+v", body)
	}
	_, body = call(t, srv, http.MethodGet, "/usb-storage", "")
	if body["ok"] != true || body["blocked"] != true {
		t.Fatalf("usb status %+v", body)
	}
	_, body = call(t, srv, http.MethodPost, "/usb-storage", `{"active":1}`)
	if body["ok"] != true || body["blocked"] != false {
		t.Fatalf("usb unblock %+v", body)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/status/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status stream %d", resp.StatusCode)
	}
	conn, ok := resp.Body.(io.ReadCloser)
	if !ok {
		t.Fatal("no body")
	}
	buf := make([]byte, 4096)
	n, err := io.ReadFull(conn, buf[:2])
	if err != nil {
		t.Fatal(err)
	}
	size := int(buf[1] & 0x7f)
	header := 2
	if size == 126 {
		if _, err = io.ReadFull(conn, buf[2:4]); err != nil {
			t.Fatal(err)
		}
		size = int(buf[2])<<8 | int(buf[3])
		header = 4
	}
	if _, err = io.ReadFull(conn, buf[header:header+size]); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	n = header + size
	frame := buf[:n]
	if frame[0]&0x0f != 0x1 {
		t.Fatalf("status frame %x", frame)
	}
	var event map[string]any
	if err = json.Unmarshal(frame[header:header+size], &event); err != nil {
		t.Fatalf("status payload %v %s", err, frame[header:header+size])
	}
	streamVersion, _ := event["version"].(map[string]any)
	if event["event"] != "status" || event["service_status"] != "running" || streamVersion["platform"] != "linux/amd64" {
		t.Fatalf("status event %+v", event)
	}
	// devicesListWSHeartbeatPayload builds a map whose only field is
	// timestamp, formatted with the 25-byte Go layout. The caller then
	// names the event heartbeat. A client ping is the 9-byte word.
	ws := dialWS(t, srv, "/devices_list/ws")
	defer ws.Close()
	_ = readWSJSON(t, ws)
	masked := maskWS([]byte("heartbeat"), maskKey)
	frame = append([]byte{0x81, byte(0x80 | len(masked))}, maskKey[:]...)
	frame = append(frame, masked...)
	if _, err = ws.Write(frame); err != nil {
		t.Fatal(err)
	}
	hb := readWSJSON(t, ws)
	if hb["event"] != "heartbeat" {
		t.Fatalf("heartbeat event %+v", hb)
	}
	stamp, _ := hb["timestamp"].(string)
	if _, err = time.Parse(time.RFC3339, stamp); err != nil {
		t.Fatalf("heartbeat timestamp %q %+v", stamp, hb)
	}
	code, _ = call(t, srv, http.MethodGet, "/version", "")
	if code != 200 {
		t.Fatal(code)
	}
	code, docsBody := call(t, srv, http.MethodGet, "/docs/swagger", "")
	if code != 200 || docsBody["swagger"] != "2.0" {
		t.Fatalf("swagger %+v", docsBody)
	}
	code, docsShort := call(t, srv, http.MethodGet, "/docs", "")
	if code != 200 || docsShort["swagger"] != docsBody["swagger"] || len(docsShort["paths"].(map[string]any)) != len(docsBody["paths"].(map[string]any)) {
		t.Fatalf("docs %+v", docsShort)
	}
	_, body = call(t, srv, http.MethodGet, "/devices_list", "")
	if body["status"] != "ok" || body["error_code"] != float64(0) || body["device_count"] != float64(0) || body["msg"] != "" {
		t.Fatalf("devices %+v", body)
	}
	if _, ok := body["local_devices"].([]any); !ok {
		t.Fatalf("devices local %+v", body)
	}
	if _, ok := body["devices"].([]any); !ok {
		t.Fatalf("devices list %+v", body)
	}
}
