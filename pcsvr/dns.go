package main

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"strconv"
	"strings"
)

// serveDNS answers A queries for the tiiny.local zone from the selected
// device address. TiinyOS calls http://api.tiiny and the other *.tiiny
// names; the system resolver has to be pointed at this listener (see
// the launcher) or those names never reach the device.
func serveDNS(ctx context.Context, cfg config, s *state) {
	pc, err := listenRetry(func() (net.PacketConn, error) {
		return net.ListenPacket("udp4", net.JoinHostPort(cfg.Host, itoa(cfg.DNSPort)))
	})
	if err != nil {
		log.Printf("dns listener: %v", err)
		return
	}
	defer pc.Close()
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()
	log.Printf("dns on %s for *.%s", pc.LocalAddr(), cfg.Domain)
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		reply := answerDNS(append([]byte(nil), buf[:n]...), cfg.Domain, s.targetIP())
		if reply == nil {
			continue
		}
		_, _ = pc.WriteTo(reply, addr)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func answerDNS(req []byte, domain, ip string) []byte {
	if len(req) < 12 {
		return nil
	}
	id := binary.BigEndian.Uint16(req[:2])
	flags := binary.BigEndian.Uint16(req[2:4])
	qd := binary.BigEndian.Uint16(req[4:6])
	if flags&0x8000 != 0 || qd != 1 {
		return nil
	}
	name, off, ok := readName(req, 12)
	if !ok || off+4 > len(req) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(req[off : off+2])
	qclass := binary.BigEndian.Uint16(req[off+2 : off+4])
	if qclass != 1 {
		return nil
	}
	// A and ANY only. Anything else gets an empty NOERROR so the
	// resolver does not wait out the timeout.
	if qtype != 1 && qtype != 255 {
		return dnsHeader(id, 0x8400, req[:off+4])
	}
	if !nameInZone(name, domain) && name != "tiiny" && !strings.HasSuffix(name, ".tiiny") {
		return dnsHeader(id, 0x8403, req[:off+4]) // NXDOMAIN
	}
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return dnsHeader(id, 0x8403, req[:off+4])
	}
	out := dnsHeader(id, 0x8400, req[:off+4])
	// one answer
	binary.BigEndian.PutUint16(out[6:8], 1)
	out = append(out, 0xC0, 0x0C) // pointer to the question name
	out = append(out, 0, 1)       // A
	out = append(out, 0, 1)       // IN
	ttl := make([]byte, 4)
	binary.BigEndian.PutUint32(ttl, dnsTTL)
	out = append(out, ttl...)
	out = append(out, 0, 4)
	out = append(out, parsed...)
	return out
}

func dnsHeader(id, flags uint16, question []byte) []byte {
	out := make([]byte, len(question))
	copy(out, question)
	binary.BigEndian.PutUint16(out[0:2], id)
	binary.BigEndian.PutUint16(out[2:4], flags)
	binary.BigEndian.PutUint16(out[6:8], 0)
	binary.BigEndian.PutUint16(out[8:10], 0)
	binary.BigEndian.PutUint16(out[10:12], 0)
	return out
}

func nameInZone(name, domain string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")
	return name == domain || strings.HasSuffix(name, "."+domain)
}

func readName(msg []byte, off int) (string, int, bool) {
	var labels []string
	jumps := 0
	end := off
	moved := false
	for {
		if off >= len(msg) || jumps > 8 {
			return "", 0, false
		}
		n := int(msg[off])
		if n == 0 {
			if !moved {
				end = off + 1
			}
			return strings.Join(labels, "."), end, true
		}
		if n&0xC0 == 0xC0 {
			if off+1 >= len(msg) {
				return "", 0, false
			}
			ptr := int(n&0x3F)<<8 | int(msg[off+1])
			if !moved {
				end = off + 2
				moved = true
			}
			off = ptr
			jumps++
			continue
		}
		if n&0xC0 != 0 || off+1+n > len(msg) {
			return "", 0, false
		}
		labels = append(labels, string(msg[off+1:off+1+n]))
		off += 1 + n
		if !moved {
			end = off
		}
	}
}
