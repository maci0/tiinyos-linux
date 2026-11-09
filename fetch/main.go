// Command fetch installs the official TiinyOS app, and Electron, under a
// data directory.
//
//	fetch app <dir>       newest TiinyOS app; prints the version directory
//	fetch electron <dir>  pinned Electron runtime; prints the binary path
//
// app asks the endpoint the TiinyOS updater itself asks
// (/update/check_update) for the newest Windows x64 build, downloads it
// from Tiiny's CDN when it is newer than the installed one, and extracts
// app.asar and app.asar.unpacked into <dir>/<version> with the 7zz next
// to this executable. <dir>/current points at the version in use; the
// previous one is kept. When the check or the download fails, the
// installed version is used. With TIINY_APP_UPDATE=0 and a version
// installed, no check is made, so current can be pointed at the kept
// older version. Nothing from the package is redistributed: every user
// fetches it from Tiiny.
//
// The Windows x64 build is used: the macOS package exists only for arm64.
// Neither ships a Linux sharp addon. The native one from npm segfaults
// under Electron (its bundled GLib clashes with the GLib Electron links),
// so app adds sharp's WebAssembly build instead: @img/sharp-wasm32 at the
// asar's sharp version and its @emnapi/runtime, from the npm registry,
// checked against the registry's sha512, into <dir>/<version>/node_modules,
// which Node searches after the asar's own. That step is best effort:
// without it the app's only sharp caller (OcrUtils) logs a warning and
// uses the original image. The app's logo is copied to
// <dir>/<version>/icon.png for the desktop entry.
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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	packagePlatform = "windows"
	packageArch     = "amd64"
	// A version the service always reports as outdated, used before any
	// install so the answer carries the download URL.
	noVersion    = "0.0.0"
	httpTimeout  = 60 * time.Second
	keepVersions = 2
	// The app was built with Electron 37 and is checked on this release.
	electronVersion = "40.10.6"
	sharpWasm       = "@img/sharp-wasm32"
	emnapi          = "@emnapi/runtime"
	iconInAsar      = "out/main/tiiny-logo.png"
	userAgent       = "tiinyos-linux"
	maxJSONBytes    = 1 << 20
	maxNpmTarball   = 64 << 20
)

// Endpoints; tests point them at local servers.
var (
	checkURL     = "https://service.online.tiiny-svr.com/update/check_update"
	electronBase = "https://github.com/electron/electron/releases/download/v" + electronVersion
	npmRegistry  = "https://registry.npmjs.org"
	// No overall timeout (the installer is large); a server that does not
	// start answering within httpTimeout fails.
	client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: httpTimeout}}
	// extractPackage copies app.asar and its unpacked tree from the
	// installer into stage; tests replace it.
	extractPackage = extractWindows
)

var electronZip = "electron-v" + electronVersion + "-linux-x64.zip"

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fetch: "+format+"\n", args...)
}

// versionKey parses 1.0.3 (a leading v and any -/+ suffix ignored).
func versionKey(version string) ([]int, error) {
	core := strings.TrimLeft(strings.TrimSpace(version), "vV")
	core, _, _ = strings.Cut(core, "-")
	core, _, _ = strings.Cut(core, "+")
	var key []int
	for part := range strings.SplitSeq(core, ".") {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || part == "" || strings.TrimLeft(part, "0123456789") != "" {
			return nil, fmt.Errorf("not a version: %q", version)
		}
		key = append(key, n)
	}
	return key, nil
}

// newer reports whether version a sorts after b; both must parse.
func newer(a, b string) bool {
	ka, _ := versionKey(a)
	kb, _ := versionKey(b)
	for i := 0; i < len(ka) || i < len(kb); i++ {
		x, y := 0, 0
		if i < len(ka) {
			x = ka[i]
		}
		if i < len(kb) {
			y = kb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func get(url string, body []byte) (*http.Response, error) {
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		method, reader = http.MethodPost, bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

func getJSON(url string, body []byte, into any) error {
	resp, err := get(url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxJSONBytes)).Decode(into)
}

type release struct {
	version, url string
	size         int64
}

// latest is the newer package the update service offers, or nil.
func latest(installed string) (*release, error) {
	body, _ := json.Marshal(map[string]string{
		"arch": packageArch, "platform": packagePlatform, "now_version": installed,
		"current_os_version": installed, "current_service_version": installed,
	})
	var doc struct {
		NeedUpdate    bool   `json:"need_update"`
		NewestVersion string `json:"newest_version"`
		DownloadsURL  string `json:"downloads_url"`
		TotalBytes    int64  `json:"total_bytes"`
	}
	if err := getJSON(checkURL, body, &doc); err != nil {
		return nil, fmt.Errorf("update check: %w", err)
	}
	if !doc.NeedUpdate {
		return nil, nil
	}
	if !strings.HasPrefix(doc.DownloadsURL, "https://") || doc.TotalBytes <= 0 {
		return nil, fmt.Errorf("update check: incomplete reply %+v", doc)
	}
	if _, err := versionKey(doc.NewestVersion); err != nil || strings.ContainsAny(doc.NewestVersion, "/\\") || strings.HasPrefix(doc.NewestVersion, ".") {
		return nil, fmt.Errorf("update check: bad version %q", doc.NewestVersion)
	}
	return &release{doc.NewestVersion, doc.DownloadsURL, doc.TotalBytes}, nil
}

// download writes url to dest via dest.part; size < 0 skips the check.
func download(url, dest string, size int64) error {
	logf("downloading %s", url)
	resp, err := get(url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	part := dest + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	got, copyErr := io.Copy(out, resp.Body)
	if err := errors.Join(copyErr, out.Close()); err != nil {
		os.Remove(part)
		return err
	}
	if size >= 0 && got != size {
		os.Remove(part)
		return fmt.Errorf("%s: got %d bytes, expected %d", url, got, size)
	}
	return os.Rename(part, dest)
}

func installedVersion(root string) string {
	link, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		return ""
	}
	if info, err := os.Stat(filepath.Join(root, link, "app.asar")); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return link
}

// asarFile reads one file from an asar archive (fs.ErrNotExist when absent).
func asarFile(asar, inner string) ([]byte, error) {
	f, err := os.Open(asar)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Pickle: 4-byte size of the header pickle, then that pickle: 4-byte
	// payload size, 4-byte JSON length, JSON, padding.
	var prefix [16]byte
	if _, err := io.ReadFull(f, prefix[:]); err != nil {
		return nil, fmt.Errorf("%s: %w", asar, err)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	pickleSize := int64(binary.LittleEndian.Uint32(prefix[4:8]))
	jsonLen := int64(binary.LittleEndian.Uint32(prefix[12:16]))
	if jsonLen > info.Size() {
		return nil, fmt.Errorf("%s: header longer than the file", asar)
	}
	header := make([]byte, jsonLen)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, fmt.Errorf("%s: header: %w", asar, err)
	}
	type node struct {
		Files  map[string]*node `json:"files"`
		Offset string           `json:"offset"`
		Size   int64            `json:"size"`
	}
	var root node
	if err := json.Unmarshal(header, &root); err != nil {
		return nil, fmt.Errorf("%s: header: %w", asar, err)
	}
	cur := &root
	for part := range strings.SplitSeq(inner, "/") {
		next := cur.Files[part]
		if next == nil {
			return nil, fmt.Errorf("%s in %s: %w", inner, asar, fs.ErrNotExist)
		}
		cur = next
	}
	offset, err := strconv.ParseInt(cur.Offset, 10, 64)
	if err != nil || cur.Files != nil {
		return nil, fmt.Errorf("%s in %s: %w", inner, asar, fs.ErrNotExist)
	}
	if cur.Size < 0 || offset < 0 || cur.Size > info.Size() {
		return nil, fmt.Errorf("%s in %s: bad size or offset", inner, asar)
	}
	data := make([]byte, cur.Size)
	if _, err := f.ReadAt(data, 8+pickleSize+offset); err != nil {
		return nil, fmt.Errorf("%s in %s: %w", inner, asar, err)
	}
	return data, nil
}

type npmPackage struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Dependencies map[string]string `json:"dependencies"`
	Dist         struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
	} `json:"dist"`
}

func npmMeta(name, version string) (*npmPackage, error) {
	var meta npmPackage
	if err := getJSON(npmRegistry+"/"+name+"/"+version, nil, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// npmInstall unpacks one registry package into modules/<name> after
// checking its sha512 integrity.
func npmInstall(meta *npmPackage, modules string) error {
	algo, want, _ := strings.Cut(meta.Dist.Integrity, "-")
	if algo != "sha512" {
		return fmt.Errorf("%s: unsupported integrity %q", meta.Name, algo)
	}
	resp, err := get(meta.Dist.Tarball, nil)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxNpmTarball))
	resp.Body.Close()
	if err != nil {
		return err
	}
	sum := sha512.Sum512(data)
	if base64.StdEncoding.EncodeToString(sum[:]) != want {
		return fmt.Errorf("%s: tarball does not match its integrity", meta.Name)
	}
	dest := filepath.Join(modules, filepath.FromSlash(meta.Name))
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := strings.CutPrefix(hdr.Name, "package/")
		if !ok || hdr.Typeflag != tar.TypeReg {
			continue
		}
		target, err := within(dest, rel)
		if err != nil {
			return fmt.Errorf("%s: %w", meta.Name, err)
		}
		if err := writeFile(target, tr, 0o644); err != nil {
			return err
		}
	}
}

// within joins rel under root and refuses a path that leaves root.
func within(root, rel string) (string, error) {
	clean := path.Clean(rel)
	if path.IsAbs(rel) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe path %q", rel)
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

func writeFile(target string, src io.Reader, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, src)
	return errors.Join(copyErr, out.Close())
}

// caretOK reports whether version satisfies wanted (^X.Y.Z or exact).
func caretOK(version, wanted string) bool {
	low, ok := strings.CutPrefix(wanted, "^")
	if !ok {
		return version == wanted
	}
	have, err1 := versionKey(version)
	min, err2 := versionKey(low)
	return err1 == nil && err2 == nil && have[0] == min[0] && !newer(low, version)
}

func addSharpWasm(versionDir string) error {
	raw, err := asarFile(filepath.Join(versionDir, "app.asar"), "node_modules/sharp/package.json")
	if err != nil {
		return err
	}
	var sharp struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &sharp); err != nil {
		return err
	}
	wasm, err := npmMeta(sharpWasm, sharp.Version)
	if err != nil {
		return err
	}
	runtime, err := npmMeta(emnapi, "latest")
	if err != nil {
		return err
	}
	if wanted := wasm.Dependencies[emnapi]; !caretOK(runtime.Version, wanted) {
		return fmt.Errorf("%s %s does not satisfy %q", emnapi, runtime.Version, wanted)
	}
	stage := filepath.Join(versionDir, ".node_modules.tmp")
	os.RemoveAll(stage)
	if err := npmInstall(wasm, stage); err != nil {
		return err
	}
	if err := npmInstall(runtime, stage); err != nil {
		return err
	}
	final := filepath.Join(versionDir, "node_modules")
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	logf("added %s %s and %s %s", sharpWasm, sharp.Version, emnapi, runtime.Version)
	return os.Rename(stage, final)
}

// addExtras adds the icon and WebAssembly sharp, each once per version.
func addExtras(versionDir string) {
	icon := filepath.Join(versionDir, "icon.png")
	if _, err := os.Stat(icon); err != nil {
		data, err := asarFile(filepath.Join(versionDir, "app.asar"), iconInAsar)
		if err == nil {
			err = os.WriteFile(icon, data, 0o644)
		}
		if err != nil {
			logf("no icon: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(versionDir, "node_modules", filepath.FromSlash(sharpWasm))); err != nil {
		if err := addSharpWasm(versionDir); err != nil {
			logf("image module not added, the app runs without it: %v", err)
		}
	}
}

func sha256File(name string) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func install(root string, rel *release) error {
	downloads := filepath.Join(root, "download")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		return err
	}
	pkg := filepath.Join(downloads, path.Base(rel.url))
	if info, err := os.Stat(pkg); err != nil || info.Size() != rel.size {
		if err := download(rel.url, pkg, rel.size); err != nil {
			return err
		}
	}
	stage := filepath.Join(root, "."+rel.version+".tmp")
	os.RemoveAll(stage)
	manifest, err := extractPackage(pkg, stage)
	if err != nil {
		os.RemoveAll(stage)
		return err
	}
	if _, err := asarFile(filepath.Join(stage, "app.asar"), "out/main/index.js"); err != nil {
		os.RemoveAll(stage)
		return fmt.Errorf("app.asar has no out/main/index.js: %w", err)
	}
	manifest["version"], manifest["url"] = rel.version, rel.url
	doc, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), append(doc, '\n'), 0o644); err != nil {
		return err
	}
	final := filepath.Join(root, rel.version)
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	link := filepath.Join(root, ".current.tmp")
	os.Remove(link)
	if err := os.Symlink(rel.version, link); err != nil {
		return err
	}
	if err := os.Rename(link, filepath.Join(root, "current")); err != nil {
		return err
	}
	logf("installed TiinyOS %s", rel.version)
	return os.Remove(pkg)
}

// prune keeps current and the keepVersions newest version directories.
func prune(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var versions []string
	for _, e := range entries {
		if _, err := versionKey(e.Name()); err == nil && e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	sort.Slice(versions, func(i, j int) bool { return newer(versions[i], versions[j]) })
	keep := map[string]bool{installedVersion(root): true}
	for i := 0; i < len(versions) && i < keepVersions; i++ {
		keep[versions[i]] = true
	}
	for _, v := range versions {
		if !keep[v] {
			if err := os.RemoveAll(filepath.Join(root, v)); err != nil {
				return err
			}
		}
	}
	return nil
}

func app(root string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	have := installedVersion(root)
	if have == "" || os.Getenv("TIINY_APP_UPDATE") != "0" {
		err := func() error {
			from := have
			if from == "" {
				from = noVersion
			}
			rel, err := latest(from)
			if err != nil || rel == nil || have != "" && !newer(rel.version, have) {
				return err
			}
			if err := install(root, rel); err != nil {
				return err
			}
			return prune(root)
		}()
		if err != nil && have == "" {
			return "", fmt.Errorf("no TiinyOS installed and the download failed: %w", err)
		}
		if err != nil {
			logf("update failed, staying on %s: %v", have, err)
		}
	}
	version := installedVersion(root)
	if version == "" {
		return "", errors.New("no TiinyOS installed")
	}
	dir := filepath.Join(root, version)
	addExtras(dir)
	return dir, nil
}

func electron(root string) (string, error) {
	target := filepath.Join(root, "electron-"+electronVersion)
	binary := filepath.Join(target, "electron")
	if info, err := os.Stat(binary); err == nil && info.Mode().IsRegular() {
		return binary, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	archive := filepath.Join(root, electronZip)
	if err := download(electronBase+"/"+electronZip, archive, -1); err != nil {
		return "", err
	}
	defer os.Remove(archive)
	resp, err := get(electronBase+"/SHASUMS256.txt", nil)
	if err != nil {
		return "", err
	}
	sums, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONBytes))
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	want := ""
	for line := range strings.SplitSeq(string(sums), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == "*"+electronZip {
			want = fields[0]
		}
	}
	got, _, err := sha256File(archive)
	if err != nil {
		return "", err
	}
	if want == "" || got != want {
		return "", fmt.Errorf("%s does not match SHASUMS256.txt", electronZip)
	}
	stage := filepath.Join(root, ".electron-"+electronVersion+".tmp")
	os.RemoveAll(stage)
	if err := unzip(archive, stage); err != nil {
		return "", err
	}
	if err := os.Rename(stage, target); err != nil {
		return "", err
	}
	logf("installed Electron %s", electronVersion)
	return binary, nil
}

// unzip extracts a zip archive into dest, keeping file modes and
// symbolic links, refusing paths that leave dest.
func unzip(archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := within(dest, f.Name)
		if err != nil {
			return err
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			err = os.MkdirAll(target, 0o755)
		case mode&fs.ModeSymlink != 0:
			err = unzipLink(f, target)
		default:
			err = unzipFile(f, target, mode.Perm())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func unzipFile(f *zip.File, target string, perm fs.FileMode) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	if perm == 0 {
		perm = 0o644
	}
	return writeFile(target, src, perm)
}

func unzipLink(f *zip.File, target string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	dest, err := io.ReadAll(io.LimitReader(src, 4096))
	src.Close()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(string(dest), target)
}

// sevenZip is the 7zz shipped next to this executable.
func sevenZip() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(self), "7zz"), nil
}

// run7z runs 7zz; Go has no reader for the NSIS installer format, so this
// shells out to the 7-Zip binary shipped beside fetch.
func run7z(args ...string) error {
	bin, err := sevenZip()
	if err != nil {
		return err
	}
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("7zz %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// extractWindows copies app.asar and app.asar.unpacked out of the NSIS
// installer into stage and describes what it found.
func extractWindows(pkg, stage string) (map[string]any, error) {
	head := make([]byte, 2)
	if f, err := os.Open(pkg); err == nil {
		_, err = io.ReadFull(f, head)
		f.Close()
		if err != nil || string(head) != "MZ" {
			return nil, fmt.Errorf("%s is not a Windows installer", pkg)
		}
	} else {
		return nil, err
	}
	// Next to stage, not in /tmp: the installer unpacks to hundreds of MB.
	work, err := os.MkdirTemp(filepath.Dir(stage), ".tiiny-extract-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if err := run7z("x", "-y", "-o"+work, pkg, "$PLUGINSDIR/app-64.7z"); err != nil {
		return nil, err
	}
	archives := findNamed(work, func(name string, dir bool) bool { return !dir && strings.HasSuffix(name, ".7z") })
	if len(archives) != 1 {
		return nil, fmt.Errorf("expected one payload archive, found %v", archives)
	}
	appRoot := filepath.Join(work, "app")
	if err := run7z("x", "-y", "-o"+appRoot, archives[0]); err != nil {
		return nil, err
	}
	asars := findNamed(appRoot, func(name string, dir bool) bool { return !dir && name == "app.asar" })
	if len(asars) != 1 {
		return nil, fmt.Errorf("expected one app.asar, found %v", asars)
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return nil, err
	}
	if err := copyTree(asars[0], filepath.Join(stage, "app.asar")); err != nil {
		return nil, err
	}
	manifest := map[string]any{"kind": "windows-nsis", "package": filepath.Base(pkg)}
	manifest["app.asar"], _ = filepath.Rel(appRoot, asars[0])
	manifest["app.asar.unpacked"] = ""
	if unpacked := findNamed(appRoot, func(name string, dir bool) bool { return dir && name == "app.asar.unpacked" }); len(unpacked) > 0 {
		if err := copyTree(unpacked[0], filepath.Join(stage, "app.asar.unpacked")); err != nil {
			return nil, err
		}
		manifest["app.asar.unpacked"], _ = filepath.Rel(appRoot, unpacked[0])
	}
	var errs []error
	manifest["asar_sha256"], manifest["asar_bytes"], err = sha256File(asars[0])
	errs = append(errs, err)
	manifest["package_sha256"], manifest["package_bytes"], err = sha256File(pkg)
	errs = append(errs, err)
	return manifest, errors.Join(errs...)
}

func findNamed(root string, match func(name string, dir bool) bool) []string {
	var found []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && match(d.Name(), d.IsDir()) {
			found = append(found, p)
		}
		return err
	})
	return found
}

// copyTree copies a file or directory, keeping modes and symlinks.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			return writeFile(target, in, info.Mode().Perm())
		}
	})
}

func main() {
	if len(os.Args) != 3 || (os.Args[1] != "app" && os.Args[1] != "electron") {
		fmt.Fprintln(os.Stderr, "usage: fetch app|electron <dir>")
		os.Exit(2)
	}
	root, err := filepath.Abs(os.Args[2])
	if err == nil {
		var out string
		if os.Args[1] == "app" {
			out, err = app(root)
		} else {
			out, err = electron(root)
		}
		if err == nil {
			fmt.Println(out)
			return
		}
	}
	logf("%v", err)
	os.Exit(1)
}
