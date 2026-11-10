package main

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

// The app reads the device list by opening /devices_list/ws, taking the
// first frame and closing (main process, every poll). The server must
// answer the close frame and drop the connection, or one socket leaks
// per poll.
func TestDevicesWSClosesOnClientClose(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	ws := dialWS(t, srv, "/devices_list/ws")
	defer ws.Close()
	_ = readWSJSON(t, ws)

	payload := maskWS([]byte{0x03, 0xe8}, maskKey) // status 1000
	frame := append([]byte{0x88, byte(0x80 | len(payload))}, maskKey[:]...)
	if _, err := ws.Write(append(frame, payload...)); err != nil {
		t.Fatal(err)
	}

	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply := make([]byte, 2)
	if _, err := io.ReadFull(ws, reply); err != nil {
		t.Fatalf("no close frame back: %v", err)
	}
	if reply[0] != 0x88 {
		t.Fatalf("first byte back %#x, want a close frame", reply[0])
	}
	if _, err := io.CopyN(io.Discard, ws, int64(reply[1]&0x7f)); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal("server kept the connection open after the close handshake")
		}
		t.Fatalf("after close: %v", err)
	}
}
