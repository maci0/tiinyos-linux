package main

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"io"
)

// RFC 6455 magic. The Electron client speaks a normal WebSocket; the
// handshake is small enough to do by hand and keeps the binary free of
// a websocket dependency.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsOpClose is the close frame opcode; wsCloseNormal is its payload for
// status 1000 (normal closure).
const wsOpClose = 0x8

var wsCloseNormal = []byte{0x03, 0xe8}

func websocketAccept(key string) string {
	key = trim(key)
	if key == "" {
		return ""
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func writeWSText(w io.Writer, payload []byte) error {
	return writeWS(w, 0x1, payload)
}

// readWS reads one client frame. A masked payload is unmasked. Ping
// frames are answered with a pong and the next frame is returned.
func readWS(conn io.ReadWriter) (byte, []byte, error) {
	for {
		var hdr [2]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return 0, nil, err
		}
		opcode := hdr[0] & 0x0f
		masked := hdr[1]&0x80 != 0
		size := int(hdr[1] & 0x7f)
		switch size {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(conn, ext[:]); err != nil {
				return 0, nil, err
			}
			size = int(ext[0])<<8 | int(ext[1])
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(conn, ext[:]); err != nil {
				return 0, nil, err
			}
			size = int(binary.BigEndian.Uint64(ext[:]))
		}
		var key [4]byte
		if masked {
			if _, err := io.ReadFull(conn, key[:]); err != nil {
				return 0, nil, err
			}
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= key[i%4]
			}
		}
		if opcode == 0x9 {
			if err := writeWS(conn, 0xA, payload); err != nil {
				return 0, nil, err
			}
			continue
		}
		if opcode == wsOpClose {
			return opcode, payload, io.EOF
		}
		return opcode, payload, nil
	}
}

func writeWSPing(w io.Writer) error {
	return writeWS(w, 0x9, nil)
}

func writeWS(w io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n <= 65535:
		header = append(header, 126, byte(n>>8), byte(n))
	default:
		ext := make([]byte, 8)
		binary.BigEndian.PutUint64(ext, uint64(n))
		header = append(header, 127)
		header = append(header, ext...)
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
