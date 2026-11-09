package main

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"
)

// The device firmware on port 80 does not serve the agent catalog, the
// upgrade version API, or the NPU status socket. The app waits out each
// of those and logs a failure. These handlers answer the shapes the
// app already accepts, so the waits stop.
func registerCompat(mux *http.ServeMux) {
	emptyApps := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"app": []any{}, "categories": []any{}, "carousel": []any{}})
	}
	mux.HandleFunc("GET /api/services", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("GET /api/v1/apps", emptyApps)
	mux.HandleFunc("GET /api/v1/apps/icons", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{})
	})
	mux.HandleFunc("GET /api/services/resource-usage", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"cpu_percent": 0, "memory_percent": 0, "npu_percent": 0})
	})
	emptyModels := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": []any{}, "models": []any{}})
	}
	mux.HandleFunc("GET /api/v1/models/", emptyModels)
	mux.HandleFunc("GET /api/v1/models/online_models", emptyModels)
	mux.HandleFunc("GET /v1/chat/conversations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("GET /v1/chat/conversations/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("GET /v1/chat/messages/{id}/blocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("GET /conversations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("GET /conversations/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("GET /api/v1/sys/network", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"status": "ok", "connected": true, "type": "usb"})
	})
	mux.HandleFunc("GET /api/v1/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	// DashboardPage fetches these two and throws when the status is not
	// 200. week_usage is an object keyed by date. Each date maps model
	// names to objects with total_tokens. app_week_usage is parsed as
	// JSON and may be empty.
	mux.HandleFunc("GET /api/v1/statistics/week_usage", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{})
	})
	mux.HandleFunc("GET /api/v1/statistics/app_week_usage", func(w http.ResponseWriter, r *http.Request) {
		// DashboardPage reads this as an object keyed by app id.
		// Each value has models and total_tokens. An empty object
		// has nothing to draw. A list makes Object.entries throw.
		writeJSON(w, map[string]any{})
	})
	mux.HandleFunc("GET /api/v1/models/running/ws", runningModels)
	mux.HandleFunc("GET /api/v1/npu/status", npuStatus)
}

func runningModels(w http.ResponseWriter, r *http.Request) {
	conn, ok := upgradeWS(w, r)
	if !ok {
		return
	}
	defer conn.Close()
	_ = writeWSText(conn, mustJSON(map[string]any{
		"type":     "snapshot",
		"running":  []any{},
		"pending":  []any{},
		"event_id": 1,
	}))
	buf := make([]byte, 1024)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}

func upgradeWS(w http.ResponseWriter, r *http.Request) (net.Conn, bool) {
	hj, ok := w.(http.Hijacker)
	if !ok || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeJSON(w, map[string]any{"status": "ok"})
		return nil, false
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, false
	}
	key := websocketAccept(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		conn.Close()
		return nil, false
	}
	reply := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + key + "\r\n"
	if protocol := selectedProtocol(r.Header.Get("Sec-WebSocket-Protocol")); protocol != "" {
		reply += "Sec-WebSocket-Protocol: " + protocol + "\r\n"
	}
	reply += "\r\n"
	_, _ = rw.WriteString(reply)
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, false
	}
	return conn, true
}

func selectedProtocol(header string) string {
	for part := range strings.SplitSeq(header, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			return part
		}
	}
	return ""
}

func npuSnapshot() map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"type":             "snapshot",
		"hm_smi_available": false,
		"devices":          []any{},
		"occupants":        []any{},
		"cpu":              map[string]any{"total_percent": 0, "cores": []any{}},
		"memory":           map[string]any{"usage_percent": 0},
		"npu": map[string]any{
			"status":         "ready",
			"status_message": "",
			"reason":         nil,
			"failure":        nil,
			"started_at":     now,
			"updated_at":     now,
		},
	}
}

func npuStatus(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeJSON(w, npuSnapshot())
		return
	}
	conn, ok := upgradeWS(w, r)
	if !ok {
		return
	}
	defer conn.Close()
	_ = writeWSText(conn, mustJSON(npuSnapshot()))
	buf := make([]byte, 4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		payload := unmaskClientFrame(buf[:n])
		if payload == nil {
			continue
		}
		var msg struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &msg) != nil {
			continue
		}
		if msg.Type == "pc_timezone_update" {
			_ = writeWSText(conn, mustJSON(map[string]string{"type": "pc_timezone_update_ack"}))
		}
	}
}

func unmaskClientFrame(frame []byte) []byte {
	if len(frame) < 2 || frame[1]&0x80 == 0 {
		return nil
	}
	length := int(frame[1] & 0x7F)
	offset := 2
	switch length {
	case 126:
		if len(frame) < 4 {
			return nil
		}
		length = int(frame[2])<<8 | int(frame[3])
		offset = 4
	case 127:
		return nil
	}
	if len(frame) < offset+4+length {
		return nil
	}
	mask := frame[offset : offset+4]
	payload := frame[offset+4 : offset+4+length]
	out := make([]byte, length)
	for i := range out {
		out[i] = payload[i] ^ mask[i%4]
	}
	return out
}
