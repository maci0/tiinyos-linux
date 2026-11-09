package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestAnswerDNSWildcard(t *testing.T) {
	req := dnsQuery("api.tiiny.local", 1)
	reply := answerDNS(req, "tiiny.local", "172.17.7.177")
	if reply == nil {
		t.Fatal("no reply")
	}
	if binary.BigEndian.Uint16(reply[2:4])&0x8000 == 0 {
		t.Fatal("response flag not set")
	}
	if binary.BigEndian.Uint16(reply[6:8]) != 1 {
		t.Fatalf("ancount %d", binary.BigEndian.Uint16(reply[6:8]))
	}
	ip := net.IP(reply[len(reply)-4:])
	if ip.String() != "172.17.7.177" {
		t.Fatalf("ip %s", ip)
	}
}

func TestAnswerDNSOutsideZone(t *testing.T) {
	reply := answerDNS(dnsQuery("example.com", 1), "tiiny.local", "172.17.7.177")
	if reply == nil {
		t.Fatal("no reply")
	}
	if binary.BigEndian.Uint16(reply[2:4])&0x000F != 3 {
		t.Fatalf("rcode %d", binary.BigEndian.Uint16(reply[2:4])&0x000F)
	}
	if binary.BigEndian.Uint16(reply[6:8]) != 0 {
		t.Fatal("expected no answers")
	}
}

func TestAnswerDNSBareTiinyName(t *testing.T) {
	reply := answerDNS(dnsQuery("api.tiiny", 1), "tiiny.local", "172.29.41.121")
	if reply == nil {
		t.Fatal("no reply")
	}
	if binary.BigEndian.Uint16(reply[6:8]) != 1 {
		t.Fatal("api.tiiny was not answered")
	}
	ip := net.IP(reply[len(reply)-4:])
	if ip.String() != "172.29.41.121" {
		t.Fatalf("ip %s", ip)
	}
}

func TestAnswerDNSNoDevice(t *testing.T) {
	reply := answerDNS(dnsQuery("api.tiiny", 1), "tiiny.local", "")
	if reply == nil {
		t.Fatal("no reply")
	}
	if binary.BigEndian.Uint16(reply[6:8]) != 0 {
		t.Fatal("answered without a device address")
	}
}

func TestParseDeviceAndPublishUSB(t *testing.T) {
	raw := []byte(`{
		"device_id":"abc","device_name":"pocket",
		"ipv4_addresses":[{"interface":"wlan0","address":"192.168.1.9"}],
		"usb":{"interface":"usb0","active":1,"device_ip":"127.0.0.1","host_ip":"127.0.0.1"}
	}`)
	dev, ok := parseDevice(raw)
	if !ok {
		t.Fatal("parse")
	}
	if dev.DeviceID != "abc" {
		t.Fatalf("id %s", dev.DeviceID)
	}
	pub := publish(dev)
	if !pub.Local || !pub.USB || pub.DeviceIP != "127.0.0.1" {
		t.Fatalf("published %+v", pub)
	}
}

func TestPublishSkipsUSBWhenHostAddressMissing(t *testing.T) {
	raw := []byte(`{
		"device_id":"abc","device_name":"pocket",
		"ipv4_addresses":[{"interface":"usb0","address":"172.29.41.121"},{"interface":"wlan0","address":"192.168.1.9"}],
		"usb":{"interface":"usb0","active":1,"network":"172.29.41.120/30","device_ip":"172.29.41.121","host_ip":"203.0.113.22"}
	}`)
	dev, ok := parseDevice(raw)
	if !ok {
		t.Fatal("parse")
	}
	pub := publish(dev)
	if pub.Local || pub.USB || pub.DeviceIP != "192.168.1.9" {
		t.Fatalf("published %+v", pub)
	}
}

func TestParseDeviceUsesSerialAsID(t *testing.T) {
	raw := []byte(`{"device_id":"3823133a","device_name":"pocket","serial_number":"TNYM000000000000000","ipv4_addresses":[{"interface":"wlan0","address":"192.168.1.9"}]}`)
	dev, ok := parseDevice(raw)
	if !ok {
		t.Fatal("parse")
	}
	if dev.DeviceID != "TNYM000000000000000" {
		t.Fatalf("id %s", dev.DeviceID)
	}
}

func TestParseDeviceLANOnly(t *testing.T) {
	raw := []byte(`{"device_id":"abc","device_name":"pocket","ipv4_addresses":[{"interface":"wlan0","address":"192.168.1.9"}]}`)
	dev, ok := parseDevice(raw)
	if !ok {
		t.Fatal("parse")
	}
	pub := publish(dev)
	if pub.Local || pub.DeviceIP != "192.168.1.9" {
		t.Fatalf("published %+v", pub)
	}
}

func TestPointToPointPeer(t *testing.T) {
	_, n, err := net.ParseCIDR("172.17.7.178/30")
	if err != nil {
		t.Fatal(err)
	}
	n.IP = net.ParseIP("172.17.7.178")
	peer := pointToPointPeer(n)
	if peer == nil || peer.String() != "172.17.7.177" {
		t.Fatalf("peer %v", peer)
	}
	_, wide, _ := net.ParseCIDR("192.168.1.20/24")
	wide.IP = net.ParseIP("192.168.1.20")
	if pointToPointPeer(wide) != nil {
		t.Fatal("a /24 must not be treated as the gadget link")
	}
}

func TestCatalogRoutes(t *testing.T) {
	s := newState(config{DeviceTTL: defaultDeviceTTL, Domain: defaultDomain, DNS: true})
	mux := http.NewServeMux()
	register(mux, s)
	// The binary serves the mux through proxyOrLocal. Catalog paths
	// stay local. A device that refuses the connection must not turn
	// those paths into a gateway error.
	srv := httptest.NewServer(proxyOrLocal(mux, func() string { return "127.0.0.1:1" }))
	defer srv.Close()

	// Sign-in belongs to the box; the catalog has no stand-in for it.
	code, _ := call(t, srv, http.MethodPost, "/api/v1/connect", `{"device_id":"TNYM000000000000000"}`)
	if code != http.StatusNotFound {
		t.Fatalf("connect is not a catalog route: %d", code)
	}
	code, docsBody := call(t, srv, http.MethodGet, "/docs", "")
	paths, _ := docsBody["paths"].(map[string]any)
	if code != 200 || docsBody["swagger"] != "2.0" || len(paths) == 0 {
		t.Fatalf("docs through proxy %d %+v", code, docsBody)
	}
	code, week := call(t, srv, http.MethodGet, "/api/v1/statistics/week_usage?start_date=2026-11-03", "")
	if code != 200 || week == nil {
		t.Fatalf("week usage %d %+v", code, week)
	}
	code, appsWeek := call(t, srv, http.MethodGet, "/api/v1/statistics/app_week_usage?start_date=2026-11-03", "")
	if code != 200 || len(appsWeek) != 0 {
		t.Fatalf("app week %d %+v", code, appsWeek)
	}
	chatReq, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/chat/conversations", nil)
	if err != nil {
		t.Fatal(err)
	}
	chatResp, err := srv.Client().Do(chatReq)
	if err != nil {
		t.Fatal(err)
	}
	chatRaw, _ := io.ReadAll(chatResp.Body)
	chatResp.Body.Close()
	var chats []any
	if chatResp.StatusCode != 200 || json.Unmarshal(chatRaw, &chats) != nil {
		t.Fatalf("chat conversations %d %s", chatResp.StatusCode, chatRaw)
	}
	code, body := call(t, srv, http.MethodGet, "/api/v1/apps", "")
	apps, _ := body["app"].([]any)
	if code != 200 || apps == nil || body["categories"] == nil || body["carousel"] == nil {
		t.Fatalf("apps %d %+v", code, body)
	}
	code, body = call(t, srv, http.MethodGet, "/api/v1/apps/icons", "")
	if code != 200 || body == nil {
		t.Fatalf("icons %d %+v", code, body)
	}
	code, body = call(t, srv, http.MethodGet, "/api/v1/models/online_models", "")
	models, _ := body["models"].([]any)
	dataModels, _ := body["data"].([]any)
	if code != 200 || models == nil || dataModels == nil {
		t.Fatalf("models %d %+v", code, body)
	}
	// The agent store list is the box's; no local stand-in.
	code, _ = call(t, srv, http.MethodGet, "/api/services/apps/metadata", "")
	if code != http.StatusNotFound {
		t.Fatalf("metadata is not a catalog route: %d", code)
	}
	code, body = call(t, srv, http.MethodGet, "/api/services/resource-usage", "")
	if code != 200 || body["cpu_percent"] != float64(0) || body["memory_percent"] != float64(0) || body["npu_percent"] != float64(0) {
		t.Fatalf("usage %d %+v", code, body)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/chat/conversations", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var conv []any
	if resp.StatusCode != 200 || json.Unmarshal(raw, &conv) != nil || len(conv) != 0 {
		t.Fatalf("conversations %d %s", resp.StatusCode, raw)
	}
}

func TestOptionsPreflight(t *testing.T) {
	s := newState(config{DeviceTTL: defaultDeviceTTL, Domain: defaultDomain, DNS: true})
	mux := http.NewServeMux()
	register(mux, s)
	srv := httptest.NewServer(proxyOrLocal(mux, func() string { return "127.0.0.1:1" }))
	defer srv.Close()
	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/dns/update", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		t.Fatalf("unregistered options status %d", resp.StatusCode)
	}
	preflight := []string{
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
	}
	want := map[string]string{
		"Access-Control-Allow-Origin":      "*",
		"Access-Control-Allow-Methods":     "GET, POST, PUT, DELETE, OPTIONS",
		"Access-Control-Allow-Headers":     "Content-Type, Authorization, X-Requested-With",
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Expose-Headers":    "Content-Length",
		"Access-Control-Max-Age":           "86400",
	}
	for _, path := range preflight {
		req, err = http.NewRequest(http.MethodOptions, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err = srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent || len(raw) != 0 {
			t.Fatalf("preflight %s %d %q", path, resp.StatusCode, raw)
		}
		for name, value := range want {
			if resp.Header.Get(name) != value {
				t.Fatalf("preflight %s %s %q", path, name, resp.Header.Get(name))
			}
		}
	}
}

func TestUSBAutoEjectNilMonitor(t *testing.T) {
	s := newState(config{DeviceTTL: defaultDeviceTTL, Domain: defaultDomain, DNS: true})
	mux := http.NewServeMux()
	register(mux, s)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for _, payload := range []string{`{"action":"on"}`, `{"action":"off"}`, `{"action":"sideways"}`, `{}`, `{"enabled":true}`} {
		_, body := call(t, srv, http.MethodPost, "/account/update_usb_auto_eject", payload)
		if body["status"] != "error" || body["error_code"] != float64(50010) || body["enabled"] != false || body["supported"] != false || body["msg"] != "usb auto eject monitor is unavailable" || body["action"] != nil || body["record_count"] != nil {
			t.Fatalf("usb %s %+v", payload, body)
		}
	}
	_, body := call(t, srv, http.MethodGet, "/pcsvr/log?category=usb&event=auto_eject_unavailable&limit=5", "")
	events, _ := body["events"].([]any)
	if len(events) == 0 {
		t.Fatalf("eject log %+v", body)
	}
	ev, _ := events[0].(map[string]any)
	if ev["message"] != "USB auto eject monitor is unavailable" || ev["level"] != "info" || ev["category"] != "usb" || ev["event"] != "auto_eject_unavailable" || ev["timestamp"] == "" {
		t.Fatalf("eject data %+v", ev)
	}
}

func TestSelectAndResolveThroughMux(t *testing.T) {
	host := localIPv4(t)
	s := newState(config{DeviceTTL: defaultDeviceTTL, Domain: defaultDomain, DNS: true})
	s.observe(&device{
		DeviceID: "box", DeviceName: "pocket",
		IPv4Addresses: []ipOnIface{{Interface: "wlan0", Address: "192.0.2.9"}},
		USB:           &usbLink{Interface: "usb0", Active: 1, DeviceIP: "172.29.41.121", HostIP: host},
	})
	mux := http.NewServeMux()
	register(mux, s)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, body := call(t, srv, http.MethodPost, "/select_device", `{"device_id":"box","local":true}`)
	if code != 200 || body["status"] != "ok" || body["msg"] != "Selected discovered device for default DNS resolution" || body["local"] != true || body["selected_device_id"] != "box" || body["device_ip"] != "172.29.41.121" || body["device_name"] != "pocket" || body["auto_selected"] != false {
		t.Fatalf("select %+v", body)
	}
	_, statusBody := call(t, srv, http.MethodGet, "/status", "")
	sshBody, _ := statusBody["ssh_proxy"].(map[string]any)
	if sshBody["command"] != "ssh tiiny@172.29.41.121" {
		t.Fatalf("ssh command %+v", sshBody)
	}
	_, body = call(t, srv, http.MethodPost, "/dns/resolve", `{"hosts":["api.tiiny.local"]}`)
	recs, _ := body["records"].([]any)
	if len(recs) != 1 || recs[0].(map[string]any)["ipv4"] != "172.29.41.121" {
		t.Fatalf("selected resolve %+v", body)
	}

	off := newState(config{DeviceTTL: defaultDeviceTTL, Domain: defaultDomain, DNS: false})
	mux = http.NewServeMux()
	register(mux, off)
	srv2 := httptest.NewServer(mux)
	defer srv2.Close()
	_, body = call(t, srv2, http.MethodPost, "/dns/resolve", `{"hosts":["api.tiiny.local"]}`)
	if body["error_code"] != float64(50030) || body["msg"] != "dns manager is unavailable" {
		t.Fatalf("dns off %+v", body)
	}
	_, body = call(t, srv2, http.MethodPost, "/dns/update", `{"action":"set","fqdn":"extra.tiiny.local","ipv4":"10.1.1.1"}`)
	if body["error_code"] != float64(50031) || body["msg"] != "dns server is disabled" {
		t.Fatalf("dns update off %+v", body)
	}

	idle := newState(config{DeviceTTL: defaultDeviceTTL, Domain: defaultDomain, DNS: true})
	mux = http.NewServeMux()
	register(mux, idle)
	srv3 := httptest.NewServer(mux)
	defer srv3.Close()
	_, body = call(t, srv3, http.MethodPost, "/dns/resolve", `{"hosts":["api.tiiny.local"]}`)
	recs, _ = body["records"].([]any)
	if len(recs) != 1 || recs[0].(map[string]any)["ipv4"] != "127.0.0.1" {
		t.Fatalf("unselected resolve %+v", body)
	}
}

func localIPv4(t *testing.T) string {
	t.Helper()
	conn, err := net.Dial("udp", "192.0.2.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func TestDeviceListWebsocketDiff(t *testing.T) {
	s := newState(config{DeviceTTL: defaultDeviceTTL, Domain: defaultDomain, DNS: true})
	mux := http.NewServeMux()
	register(mux, s)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ws := dialWS(t, srv, "/devices_list/ws")
	defer ws.Close()
	first := readWSJSON(t, ws)
	if first["event"] != "connection" {
		t.Fatalf("first %+v", first)
	}
	s.observe(&device{
		DeviceID: "box", DeviceName: "pocket",
		IPv4Addresses: []ipOnIface{{Interface: "wlan0", Address: "192.0.2.9"}},
	})
	time.Sleep(50 * time.Millisecond)
	batch, err := readWSFrames(ws)
	if err != nil || len(batch) < 2 {
		t.Fatalf("batch %v %v", err, batch)
	}
	added := batch[0]
	addedData, _ := added["data"].(map[string]any)
	ipv4, _ := addedData["ipv4"].([]any)
	if added["event"] != "device_added" || addedData["device_id"] != "box" || addedData["connection_type"] != "network" || addedData["local"] != false || addedData["metadata_url"] != "http://192.0.2.9:39218/device.json" || len(ipv4) != 1 || ipv4[0] != "192.0.2.9" {
		t.Fatalf("added %+v", added)
	}
	selected := batch[1]
	selectedData, _ := selected["data"].(map[string]any)
	if selected["event"] != "device_selected" || selectedData["device_id"] != "box" || selectedData["local"] != false || selectedData["reachable"] != true {
		t.Fatalf("selected %+v", selected)
	}

	host := localIPv4(t)
	s.observe(&device{
		DeviceID: "box", DeviceName: "pocket",
		IPv4Addresses: []ipOnIface{{Interface: "wlan0", Address: "192.0.2.9"}},
		USB:           &usbLink{Interface: "usb0", Active: 1, DeviceIP: "172.29.41.121", HostIP: host},
	})
	time.Sleep(50 * time.Millisecond)
	batch, err = readWSFrames(ws)
	if err != nil || len(batch) < 2 {
		t.Fatalf("update batch %v %v", err, batch)
	}
	updated := batch[0]
	updatedData, _ := updated["data"].(map[string]any)
	updatedIPv4, _ := updatedData["ipv4"].([]any)
	if updated["event"] != "device_updated" || updatedData["device_id"] != "box" || updatedData["connection_type"] != "usb" || updatedData["device_ip"] != "172.29.41.121" || updatedData["local"] != true || updatedData["metadata_url"] != "http://172.29.41.121:39218/device.json" || len(updatedIPv4) != 2 || updatedIPv4[0] != "172.29.41.121" || updatedIPv4[1] != "192.0.2.9" {
		t.Fatalf("updated %+v", updated)
	}
	selected = batch[1]
	selectedData, _ = selected["data"].(map[string]any)
	if selected["event"] != "device_selected" || selectedData["local"] != true || selectedData["device_ip"] != "172.29.41.121" {
		t.Fatalf("reselected %+v", selected)
	}

	s.observe(&device{
		DeviceID: "box", DeviceName: "pocket-2",
		IPv4Addresses: []ipOnIface{{Interface: "wlan0", Address: "192.0.2.10"}},
		USB:           &usbLink{Interface: "usb0", Active: 1, DeviceIP: "172.29.41.121", HostIP: host},
	})
	time.Sleep(50 * time.Millisecond)
	batch, err = readWSFrames(ws)
	if err != nil || len(batch) < 1 {
		t.Fatalf("rename batch %v %v", err, batch)
	}
	renamed := batch[0]
	renamedData, _ := renamed["data"].(map[string]any)
	fields, _ := renamedData["changed_fields"].([]any)
	if renamed["event"] != "device_updated" || renamedData["device_name"] != "pocket-2" || renamedData["device_ip"] != "172.29.41.121" || len(fields) != 2 || fields[0] != "ipv4" || fields[1] != "device_name" {
		t.Fatalf("renamed %+v", renamed)
	}

	s.mu.Lock()
	delete(s.found, "box")
	s.selected = ""
	s.mu.Unlock()
	s.publishChange()
	time.Sleep(50 * time.Millisecond)
	batch, err = readWSFrames(ws)
	if err != nil || len(batch) < 1 {
		t.Fatalf("gone batch %v %v", err, batch)
	}
	gone := batch[0]
	goneData, _ := gone["data"].(map[string]any)
	if gone["event"] != "device_unreachable" || goneData["device_id"] != "box" {
		t.Fatalf("unreachable %+v", gone)
	}
}

func readWSFrames(conn net.Conn) ([]map[string]any, error) {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if n == 0 {
		return nil, err
	}
	raw := buf[:n]
	var events []map[string]any
	for len(raw) >= 2 {
		size := int(raw[1] & 0x7f)
		header := 2
		if size == 126 {
			if len(raw) < 4 {
				break
			}
			size = int(raw[2])<<8 | int(raw[3])
			header = 4
		}
		if len(raw) < header+size {
			break
		}
		var event map[string]any
		if err = json.Unmarshal(raw[header:header+size], &event); err != nil {
			return events, err
		}
		events = append(events, event)
		raw = raw[header+size:]
	}
	return events, nil
}

func TestDeviceExpiry(t *testing.T) {
	s := newState(config{DeviceTTL: time.Millisecond})
	s.observe(&device{DeviceID: "abc", DeviceName: "pocket", IPv4Addresses: []ipOnIface{{Interface: "wlan0", Address: "192.168.1.9"}}})
	time.Sleep(5 * time.Millisecond)
	snap := s.current()
	if len(snap.Data.Devices) != 0 || len(snap.Data.LocalDevices) != 0 {
		t.Fatalf("stale device kept: %+v", snap.Data)
	}
}

func TestWebsocketAccept(t *testing.T) {
	// RFC 6455 section 1.3 example.
	got := websocketAccept("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Fatalf("accept %s", got)
	}
}

func TestLoadConfig(t *testing.T) {
	path := t.TempDir() + "/pcsvr-api.yaml"
	body := []byte("Host: 127.0.0.1\nPort: 60000\nDeviceDiscovery:\n  UDPPort: 39217\n  RequestToken: GADGET_DISCOVER_V1\nDNS:\n  Domain: tiiny.local\n  ListenPort: 60053\n")
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(path)
	if cfg.Port != 60000 || cfg.Token != "GADGET_DISCOVER_V1" || cfg.Domain != "tiiny.local" || cfg.DNSPort != 60053 {
		t.Fatalf("%+v", cfg)
	}
}

func writeFile(path string, body []byte) error {
	return os.WriteFile(path, body, 0o644)
}

func dnsQuery(name string, qtype uint16) []byte {
	var b bytes.Buffer
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[0:2], 0x1234)
	binary.BigEndian.PutUint16(hdr[4:6], 1)
	_, _ = b.Write(hdr)
	for _, label := range splitDot(name) {
		_ = b.WriteByte(byte(len(label)))
		_, _ = b.WriteString(label)
	}
	_ = b.WriteByte(0)
	tail := make([]byte, 4)
	binary.BigEndian.PutUint16(tail[0:2], qtype)
	binary.BigEndian.PutUint16(tail[2:4], 1)
	_, _ = b.Write(tail)
	return b.Bytes()
}

func splitDot(name string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if i > start {
				out = append(out, name[start:i])
			}
			start = i + 1
		}
	}
	return out
}
