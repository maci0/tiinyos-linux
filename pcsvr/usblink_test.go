package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

// serverReply turns a client request into the server's reply of msgType
// carrying yiaddr, mask and server id, in the wire format dnsmasq uses.
func serverReply(t *testing.T, req []byte, msgType byte) []byte {
	t.Helper()
	p := append([]byte(nil), req...)
	b := p[28:]
	b[0] = 2
	copy(b[16:20], net.IPv4(172, 29, 41, 122).To4())
	opts := []byte{53, 1, msgType, 1, 4, 255, 255, 255, 252, 54, 4, 172, 29, 41, 121, 255}
	p = append(p[:28+240], opts...)
	binary.BigEndian.PutUint16(p[20:22], dhcpServerPort)
	binary.BigEndian.PutUint16(p[22:24], dhcpClientPort)
	return p
}

func TestDHCPMessageAndReply(t *testing.T) {
	mac := net.HardwareAddr{0x02, 0x99, 0x24, 0xe9, 0xfa, 0x02}
	xid := [4]byte{1, 2, 3, 4}
	msg := dhcpMessage(1, xid, mac, nil, nil)
	if ipChecksum(msg[:20]) != 0 {
		t.Fatal("IPv4 header checksum does not verify")
	}
	if !bytes.Equal(msg[16:20], net.IPv4bcast.To4()) || binary.BigEndian.Uint16(msg[22:24]) != dhcpServerPort {
		t.Fatal("DISCOVER is not addressed to 255.255.255.255:67")
	}
	b := msg[28:]
	if !bytes.Equal(b[28:34], mac) || binary.BigEndian.Uint16(b[10:12]) != 0x8000 || !bytes.Contains(b[240:], []byte{53, 1, 1}) {
		t.Fatalf("DISCOVER fields % x", b[:48])
	}
	got, ok := parseDHCPReply(serverReply(t, msg, 2), xid)
	if !ok || got.msgType != 2 || !got.addr.Equal(net.IPv4(172, 29, 41, 122)) || !got.server.Equal(net.IPv4(172, 29, 41, 121)) || net.IPMask(got.mask.To4()).String() != "fffffffc" {
		t.Fatalf("offer %+v %v", got, ok)
	}
	if _, ok := parseDHCPReply(serverReply(t, msg, 2), [4]byte{9, 9, 9, 9}); ok {
		t.Fatal("reply for another xid accepted")
	}
	req := dhcpMessage(3, xid, mac, got.addr, got.server)
	if !bytes.Contains(req[28+240:], append([]byte{50, 4}, got.addr.To4()...)) || !bytes.Contains(req[28+240:], append([]byte{54, 4}, got.server.To4()...)) {
		t.Fatal("REQUEST lacks requested address or server id")
	}
}

func FuzzParseDHCPReply(f *testing.F) {
	xid := [4]byte{1, 2, 3, 4}
	msg := dhcpMessage(1, xid, net.HardwareAddr{2, 0, 0, 0, 0, 1}, nil, nil)
	reply := append([]byte(nil), msg...)
	reply[28] = 2
	binary.BigEndian.PutUint16(reply[22:24], dhcpClientPort)
	f.Add(reply)
	f.Add([]byte{0x45})
	f.Fuzz(func(t *testing.T, p []byte) {
		_, _ = parseDHCPReply(p, xid)
	})
}

func TestUSBLinkScanStates(t *testing.T) {
	u := &usbLinkWatch{links: map[int]*usbLinkStatus{}}
	link := []net.Interface{{Index: 7, Name: "usb0"}}
	addr := false
	nmCalls := 0
	nm := func(string) error { nmCalls++; return errors.New("NetworkManager is not running") }
	has := func(net.Interface) bool { return addr }
	want := func(step, state string, attempt, calls int) {
		t.Helper()
		got := u.list()
		if len(got) != 1 || got[0].State != state || got[0].Attempt != attempt || nmCalls != calls {
			t.Fatalf("%s: %+v, nm calls %d", step, got, nmCalls)
		}
	}
	u.scan(link, has, nm)
	want("new link without address", "needs_admin", 1, 1)
	u.scan(link, has, nm)
	want("still waiting for the user", "needs_admin", 1, 1)
	addr = true
	u.scan(link, has, nm)
	want("address appeared", "configured", 1, 1)
	addr = false
	u.scan(link, has, nm)
	want("address lost", "needs_admin", 2, 2)
	u.scan(nil, has, nm)
	if len(u.list()) != 0 {
		t.Fatal("unplugged link still listed")
	}
	nm = func(string) error { nmCalls++; return nil }
	u.scan(link, has, nm)
	want("NetworkManager configures a new link", "configured", 1, 3)
}
