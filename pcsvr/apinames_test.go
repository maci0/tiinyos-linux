package main

import (
	"strings"
	"testing"
)

const usbBox = "172.29.41.121"

func TestWithHostsBlock(t *testing.T) {
	base := "127.0.0.1 localhost\n::1 localhost"
	once := withHostsBlock(base, usbBox)
	if !strings.HasPrefix(once, base+"\n") || !strings.HasSuffix(once, hostsBlock(usbBox)) {
		t.Fatalf("first write:\n%s", once)
	}
	if again := withHostsBlock(once, usbBox); again != once {
		t.Fatalf("not idempotent:\n%s", again)
	}
	moved := withHostsBlock(once+"10.0.0.1 nas\n", "192.168.1.9")
	if strings.Contains(moved, usbBox) || !strings.Contains(moved, "10.0.0.1 nas\n") || strings.Count(moved, hostsBegin) != 1 || blockAddress(moved) != "192.168.1.9" {
		t.Fatalf("moved block:\n%s", moved)
	}
	if withHostsBlock("", usbBox) != hostsBlock(usbBox) {
		t.Fatal("empty hosts")
	}
}

func TestAPINamesFor(t *testing.T) {
	block := "127.0.0.1 localhost\n" + hostsBlock(usbBox)
	own := "127.0.0.1 localhost\n10.0.0.5 openai.api.tiiny anthropic.api.tiiny ollama.api.tiiny\n"
	cases := []struct {
		name, hosts, box, want string
	}{
		{"block points at the box", block, usbBox, "ready"},
		{"box moved to Wi-Fi", block, "192.168.1.9", "needs_update"},
		{"no block", "127.0.0.1 localhost\n", usbBox, "needs_admin"},
		{"no box yet", "127.0.0.1 localhost\n", "", "no_box"},
		{"own entries", own, usbBox, "external"},
		{"own entries, no box", own, "", "external"},
		{"commented entries", "# 10.0.0.5 openai.api.tiiny anthropic.api.tiiny ollama.api.tiiny\n", usbBox, "needs_admin"},
		{"two of three names", "10.0.0.5 openai.api.tiiny anthropic.api.tiiny\n", usbBox, "needs_admin"},
	}
	for _, c := range cases {
		if got, _ := apiNamesFor(c.hosts, c.box); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestBlockAddress(t *testing.T) {
	for hosts, want := range map[string]string{
		"":                           "",
		hostsBlock(usbBox):           usbBox,
		hostsBegin + "\n" + hostsEnd: "",
		hostsBegin + "\n1.2.3.4 a\n": "",
	} {
		if got := blockAddress(hosts); got != want {
			t.Errorf("blockAddress(%q) = %q, want %q", hosts, got, want)
		}
	}
}
