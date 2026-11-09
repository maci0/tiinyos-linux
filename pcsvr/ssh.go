package main

import (
	"context"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// serveSSH accepts local connections on 127.0.0.1:60022 and copies
// them to port 22 on the selected device. The official service does
// the same so host tools can reach the box without knowing which
// address discovery just found.
func serveSSH(ctx context.Context, cfg config, s *state) {
	ln, err := listenRetry(func() (net.Listener, error) {
		return net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(sshListenPort)))
	})
	if err != nil {
		log.Printf("ssh proxy: %v", err)
		return
	}
	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	log.Printf("ssh proxy on %s", ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		go proxySSH(ctx, conn, s)
	}
}

func proxySSH(ctx context.Context, client net.Conn, s *state) {
	defer client.Close()
	ip := s.targetIP()
	if ip == "" {
		return
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	remote, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(deviceSSHPort)))
	if err != nil {
		return
	}
	defer remote.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(remote, client)
		_ = remote.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, remote)
		_ = client.Close()
	}()
	wg.Wait()
}
