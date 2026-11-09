package main

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestWithHostsBlock(t *testing.T) {
	base := "127.0.0.1 localhost\n::1 localhost"
	once := withHostsBlock(base)
	if !strings.HasPrefix(once, base+"\n") || !strings.HasSuffix(once, hostsBlock()) {
		t.Fatalf("first write:\n%s", once)
	}
	if again := withHostsBlock(once); again != once {
		t.Fatalf("not idempotent:\n%s", again)
	}
	stale := base + "\n" + hostsBegin + "\n127.0.0.1 old.api.tiiny\n" + hostsEnd + "\n10.0.0.1 nas\n"
	got := withHostsBlock(stale)
	if strings.Contains(got, "old.api.tiiny") || !strings.Contains(got, "10.0.0.1 nas\n") || strings.Count(got, hostsBegin) != 1 {
		t.Fatalf("stale block:\n%s", got)
	}
	if withHostsBlock("") != hostsBlock() {
		t.Fatal("empty hosts")
	}
}

func TestNetBindCapability(t *testing.T) {
	b := netBindCapability()
	// getcap prints this value as cap_net_bind_service=ep.
	if len(b) != 20 || binary.LittleEndian.Uint32(b) != 0x02000001 || binary.LittleEndian.Uint32(b[4:]) != 1<<10 {
		t.Fatalf("% x", b)
	}
}

func TestNamesInHosts(t *testing.T) {
	cases := map[string]bool{
		"": false,
		"172.29.41.121 openai.api.tiiny anthropic.api.tiiny ollama.api.tiiny\n":    true,
		"10.0.0.5 OpenAI.api.tiiny\n10.0.0.5 anthropic.api.tiiny ollama.api.tiiny": true,
		"# 10.0.0.5 openai.api.tiiny anthropic.api.tiiny ollama.api.tiiny\n":       false,
		"10.0.0.5 openai.api.tiiny anthropic.api.tiiny\n":                          false,
		"openai.api.tiiny 10.0.0.5\n10.0.0.5 anthropic.api.tiiny ollama.api.tiiny": false,
		hostsBlock(): true,
	}
	for hosts, want := range cases {
		if got := namesInHosts(hosts); got != want {
			t.Errorf("namesInHosts(%q) = %v", hosts, got)
		}
	}
}
