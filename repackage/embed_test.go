package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func TestExtractKeepsUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	old := payload
	defer func() { payload = old }()

	payload = zipOf(t, map[string]string{"linux/pcsvr": "binary", "linux/main.js": "v1", "linux/gone.sh": "x"})
	if err := extractPayload(dir); err != nil {
		t.Fatal(err)
	}
	pcsvr := filepath.Join(dir, "linux", "pcsvr")
	before := inode(t, pcsvr)

	payload = zipOf(t, map[string]string{"linux/pcsvr": "binary", "linux/main.js": "v2"})
	if err := extractPayload(dir); err != nil {
		t.Fatal(err)
	}
	if inode(t, pcsvr) != before {
		t.Fatal("unchanged pcsvr was rewritten")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "linux", "main.js")); string(got) != "v2" {
		t.Fatalf("main.js = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "linux", "gone.sh")); !os.IsNotExist(err) {
		t.Fatal("file dropped from the payload was kept")
	}
}
