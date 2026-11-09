package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// asarWith builds an asar holding contents (path -> bytes).
func asarWith(t *testing.T, name string, contents map[string][]byte) {
	t.Helper()
	files := map[string]any{}
	var body []byte
	names := make([]string, 0, len(contents))
	for n := range contents {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		node := files
		parts := strings.Split(n, "/")
		for _, dir := range parts[:len(parts)-1] {
			next, ok := node[dir].(map[string]any)
			if !ok {
				next = map[string]any{"files": map[string]any{}}
				node[dir] = next
			}
			node = next["files"].(map[string]any)
		}
		node[parts[len(parts)-1]] = map[string]any{"size": len(contents[n]), "offset": strconv.Itoa(len(body))}
		body = append(body, contents[n]...)
	}
	raw, _ := json.Marshal(map[string]any{"files": files})
	pad := (4 - len(raw)%4) % 4
	var out bytes.Buffer
	for _, v := range []int{4, 8 + len(raw) + pad, 4 + len(raw) + pad, len(raw)} {
		binary.Write(&out, binary.LittleEndian, uint32(v))
	}
	out.Write(raw)
	out.Write(make([]byte, pad))
	out.Write(body)
	if err := os.WriteFile(name, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const fakeInstaller = "MZ fake installer"

// service is a stand-in for the update service and Tiiny's CDN.
type service struct {
	mu     sync.Mutex
	newest string
	asked  []string
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method == http.MethodGet {
		fmt.Fprint(w, fakeInstaller)
		return
	}
	var body struct {
		NowVersion string `json:"now_version"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	s.asked = append(s.asked, body.NowVersion)
	doc := map[string]any{"need_update": newer(s.newest, body.NowVersion)}
	if doc["need_update"] == true {
		doc["newest_version"] = s.newest
		doc["total_bytes"] = len(fakeInstaller)
		doc["downloads_url"] = "https://" + r.Host + "/blobs/tiinyos-" + s.newest + ".exe"
	}
	json.NewEncoder(w).Encode(doc)
}

// withServer points the update check and the HTTP client at srv.
func withServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	saved := []any{checkURL, npmRegistry, electronBase, client, extractPackage}
	checkURL, npmRegistry, electronBase, client = srv.URL+"/update/check_update", srv.URL+"/npm", srv.URL+"/electron", srv.Client()
	t.Cleanup(func() {
		srv.Close()
		checkURL, npmRegistry, electronBase = saved[0].(string), saved[1].(string), saved[2].(string)
		client = saved[3].(*http.Client)
		extractPackage = saved[4].(func(string, string) (map[string]any, error))
	})
	return srv
}

func fakeExtract(t *testing.T, withEntry bool) func(string, string) (map[string]any, error) {
	return func(pkg, stage string) (map[string]any, error) {
		if data, _ := os.ReadFile(pkg); string(data) != fakeInstaller {
			t.Errorf("package %q", data)
		}
		os.MkdirAll(stage, 0o755)
		contents := map[string][]byte{"out/other.js": nil}
		if withEntry {
			contents = map[string][]byte{"out/main/index.js": nil}
		}
		asarWith(t, filepath.Join(stage, "app.asar"), contents)
		return map[string]any{"kind": "windows-nsis"}, nil
	}
}

func versionDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if _, err := versionKey(e.Name()); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func mustApp(t *testing.T, root, want string) {
	t.Helper()
	got, err := app(root)
	if err != nil || got != filepath.Join(root, want) {
		t.Fatalf("app() = %q, %v; want %s", got, err, want)
	}
}

func TestAppInstallUpdatePruneOffline(t *testing.T) {
	svc := &service{newest: "1.0.3"}
	srv := withServer(t, svc)
	extractPackage = fakeExtract(t, true)
	root := t.TempDir()

	mustApp(t, root, "1.0.3")
	var manifest map[string]any
	raw, _ := os.ReadFile(filepath.Join(root, "current", "manifest.json"))
	if json.Unmarshal(raw, &manifest); manifest["version"] != "1.0.3" || svc.asked[0] != noVersion {
		t.Fatalf("manifest %v, asked %v", manifest, svc.asked)
	}
	mustApp(t, root, "1.0.3") // current: no download
	if len(svc.asked) != 2 || svc.asked[1] != "1.0.3" {
		t.Fatalf("asked %v", svc.asked)
	}
	for _, v := range []string{"1.0.10", "1.1.0"} {
		svc.newest = v
		mustApp(t, root, v)
	}
	if got := versionDirs(t, root); strings.Join(got, ",") != "1.0.10,1.1.0" {
		t.Fatalf("kept %v", got)
	}
	if left, _ := os.ReadDir(filepath.Join(root, "download")); len(left) != 0 {
		t.Fatalf("download left %v", left)
	}

	// Pinned: current pointed back at the kept version stays there.
	os.Remove(filepath.Join(root, "current"))
	os.Symlink("1.0.10", filepath.Join(root, "current"))
	t.Setenv("TIINY_APP_UPDATE", "0")
	mustApp(t, root, "1.0.10")
	os.Unsetenv("TIINY_APP_UPDATE")
	mustApp(t, root, "1.1.0")

	srv.Close() // service unreachable: the installed version is used
	mustApp(t, root, "1.1.0")
}

func TestBadPackageKeepsCurrent(t *testing.T) {
	svc := &service{newest: "1.0.3"}
	withServer(t, svc)
	extractPackage = fakeExtract(t, true)
	root := t.TempDir()
	mustApp(t, root, "1.0.3")
	svc.newest = "2.0.0"
	extractPackage = fakeExtract(t, false)
	mustApp(t, root, "1.0.3")
	if got := versionDirs(t, root); strings.Join(got, ",") != "1.0.3" {
		t.Fatalf("left %v", got)
	}
}

func TestNoInstallNoServiceFails(t *testing.T) {
	srv := withServer(t, &service{newest: "1.0.3"})
	srv.Close()
	if _, err := app(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no TiinyOS installed") {
		t.Fatalf("err %v", err)
	}
}

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
		tw.Write([]byte(data))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func integrity(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// registry serves @img/sharp-wasm32 and @emnapi/runtime metadata and
// tarballs; corrupt breaks the wasm tarball's integrity.
func registry(t *testing.T, corrupt bool) http.Handler {
	wasm := tgz(t, map[string]string{"package/package.json": `{"name":"@img/sharp-wasm32"}`, "package/lib/sharp.wasm": "wasm"})
	runtime := tgz(t, map[string]string{"package/package.json": `{"name":"@emnapi/runtime"}`})
	wasmSum := integrity(wasm)
	if corrupt {
		wasmSum = integrity([]byte("other"))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "https://" + r.Host + "/npm"
		switch r.URL.Path {
		case "/npm/@img/sharp-wasm32/0.34.1":
			json.NewEncoder(w).Encode(map[string]any{"name": sharpWasm, "version": "0.34.1",
				"dependencies": map[string]string{emnapi: "^1.4.0"},
				"dist":         map[string]string{"tarball": base + "/wasm.tgz", "integrity": wasmSum}})
		case "/npm/@emnapi/runtime/latest":
			json.NewEncoder(w).Encode(map[string]any{"name": emnapi, "version": "1.5.0",
				"dist": map[string]string{"tarball": base + "/runtime.tgz", "integrity": integrity(runtime)}})
		case "/npm/wasm.tgz":
			w.Write(wasm)
		case "/npm/runtime.tgz":
			w.Write(runtime)
		default:
			http.NotFound(w, r)
		}
	})
}

func versionWithSharp(t *testing.T) string {
	dir := t.TempDir()
	asarWith(t, filepath.Join(dir, "app.asar"), map[string][]byte{
		"out/main/index.js":               nil,
		iconInAsar:                        []byte("png"),
		"node_modules/sharp/package.json": []byte(`{"version":"0.34.1"}`),
	})
	return dir
}

func TestExtrasAddIconAndSharpWasm(t *testing.T) {
	withServer(t, registry(t, false))
	dir := versionWithSharp(t)
	addExtras(dir)
	if icon, _ := os.ReadFile(filepath.Join(dir, "icon.png")); string(icon) != "png" {
		t.Fatalf("icon %q", icon)
	}
	for _, f := range []string{"@img/sharp-wasm32/lib/sharp.wasm", "@emnapi/runtime/package.json"} {
		if _, err := os.Stat(filepath.Join(dir, "node_modules", f)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBadIntegrityLeavesAppRunnable(t *testing.T) {
	withServer(t, registry(t, true))
	dir := versionWithSharp(t)
	addExtras(dir)
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules added despite a bad integrity")
	}
	if _, err := os.Stat(filepath.Join(dir, "icon.png")); err != nil {
		t.Fatal(err)
	}
}

func TestVersions(t *testing.T) {
	if !newer("1.0.10", "1.0.9") || !newer("v2.0.0-beta", "1.9") || newer("1.0", "1.0.0") {
		t.Fatal("ordering")
	}
	for _, bad := range []string{"", "1..0", "1.x", "-1.0", "1.0/2", "1. 0"} {
		if _, err := versionKey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for v, want := range map[string]bool{"1.5.0": true, "1.4.0": true, "1.3.9": false, "2.0.0": false} {
		if caretOK(v, "^1.4.0") != want {
			t.Errorf("caretOK(%s)", v)
		}
	}
}

func TestWithinRejectsEscapes(t *testing.T) {
	for _, bad := range []string{"../x", "a/../../x", "/etc/passwd", "..", "."} {
		if _, err := within("/root", bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got, err := within("/root", "a/b/../c"); err != nil || got != "/root/a/c" {
		t.Fatalf("%q %v", got, err)
	}
}

// electronZipBytes is a zip with an executable, a plain file and a symlink.
func electronZipBytes(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, mode os.FileMode, data string) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(data))
	}
	add("electron", 0o755, "#!bin")
	add("resources/default_app.asar", 0o644, "asar")
	add("libfoo.so", os.ModeSymlink|0o777, "libfoo.so.1")
	zw.Close()
	return buf.Bytes()
}

func TestElectronChecksAndUnpacks(t *testing.T) {
	data := electronZipBytes(t)
	sum := sha256.Sum256(data)
	sums := hex.EncodeToString(sum[:]) + " *" + electronZip + "\n"
	withServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/electron/" + electronZip:
			w.Write(data)
		case "/electron/SHASUMS256.txt":
			fmt.Fprint(w, sums)
		}
	}))
	root := t.TempDir()
	bin, err := electron(root)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(bin); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("electron binary %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(filepath.Dir(bin), "libfoo.so")); err != nil || link != "libfoo.so.1" {
		t.Fatalf("symlink %q %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(root, electronZip)); !os.IsNotExist(err) {
		t.Fatal("zip left behind")
	}

	sums = strings.Repeat("0", 64) + " *" + electronZip + "\n"
	if _, err := electron(t.TempDir()); err == nil || !strings.Contains(err.Error(), "SHASUMS256") {
		t.Fatalf("bad checksum accepted: %v", err)
	}
}

func FuzzAsarFile(f *testing.F) {
	dir := f.TempDir()
	seed := filepath.Join(dir, "seed.asar")
	tt := &testing.T{}
	asarWith(tt, seed, map[string][]byte{"out/main/index.js": []byte("x")})
	data, _ := os.ReadFile(seed)
	f.Add(data)
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		name := filepath.Join(t.TempDir(), "f.asar")
		os.WriteFile(name, data, 0o644)
		asarFile(name, "out/main/index.js")
	})
}
