package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"strconv"
	"strings"
)

// Linux entries for Agent Store integrations. The box's store metadata
// (agent-services /api/services/apps/metadata) lists desktop
// integrations for macOS and Windows only. For a tool with an official
// Linux build, pcsvr adds a linux-x86-64 platform: Tiiny's macOS script
// bundle (the app downloads it from Tiiny, as on macOS) with the tool
// download pointed at the project's own Linux release. main.js swaps in
// this launcher's Linux scripts where the macOS ones cannot run. An entry
// is added only while Tiiny's macOS bundle is for the pinned version.

const (
	storeMetadataHost = "agent-services.api.tiiny"
	storeMetadataPath = "/api/services/apps/metadata"
	linuxPlatform     = "linux-x86-64"
	bundlePlatform    = "darwin-arm64"
	maxStoreMetadata  = 8 << 20
)

type linuxArtifact struct {
	appID, version, filename, url, sha256 string
	size                                  int64
}

var linuxArtifacts = []linuxArtifact{
	{
		appID: "opencode", version: "v1.18.35", filename: "opencode-linux-x64.tar.gz",
		url:    "https://github.com/anomalyco/opencode/releases/download/v1.18.35/opencode-linux-x64.tar.gz",
		sha256: "c8f888b451f5494a18f858fffb0e0b68f4e4baa9c241761c5f206884f0fa640d", size: 60669097,
	},
}

func isStoreMetadata(req *http.Request) bool {
	return hostOnly(req.Host) == storeMetadataHost && req.URL.Path == storeMetadataPath
}

// hostOnly is host without a port.
func hostOnly(host string) string {
	if name, _, ok := strings.Cut(host, ":"); ok {
		return name
	}
	return host
}

// patchStoreMetadata replaces resp's body with the metadata plus the
// Linux entries. A body it cannot read as plain JSON passes unchanged.
func patchStoreMetadata(resp *http.Response) error {
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxStoreMetadata+1))
	resp.Body.Close()
	if err != nil {
		return err
	}
	out := raw
	if len(raw) <= maxStoreMetadata {
		if patched, added, err := withLinuxIntegrations(raw); err != nil {
			log.Printf("store: metadata left unchanged: %v", err)
		} else {
			out = patched
			if len(added) > 0 {
				log.Printf("store: added %s for %s", linuxPlatform, strings.Join(added, ", "))
			}
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}

// withLinuxIntegrations adds a linux-x86-64 platform to the app list and
// to integration_body for each linuxArtifacts entry whose darwin-arm64
// bundle is for the pinned version. Unknown fields are kept as they are.
func withLinuxIntegrations(raw []byte) ([]byte, []string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	var apps, bodies []map[string]json.RawMessage
	if err := json.Unmarshal(doc["app"], &apps); err != nil {
		return nil, nil, fmt.Errorf("app: %w", err)
	}
	if err := json.Unmarshal(doc["integration_body"], &bodies); err != nil {
		return nil, nil, fmt.Errorf("integration_body: %w", err)
	}
	var added []string
	for _, art := range linuxArtifacts {
		body := findByAppID(bodies, art.appID)
		app := findByAppID(apps, art.appID)
		if body == nil || app == nil {
			continue
		}
		ok, err := addLinuxIntegration(body, art)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", art.appID, err)
		}
		if !ok {
			continue
		}
		if err := addLinuxAppPlatform(app, art); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", art.appID, err)
		}
		added = append(added, art.appID)
	}
	var err error
	if doc["app"], err = json.Marshal(apps); err != nil {
		return nil, nil, err
	}
	if doc["integration_body"], err = json.Marshal(bodies); err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(doc)
	return out, added, err
}

func findByAppID(list []map[string]json.RawMessage, id string) map[string]json.RawMessage {
	for _, item := range list {
		var got string
		if json.Unmarshal(item["app_id"], &got) == nil && got == id {
			return item
		}
	}
	return nil
}

// platformList decodes item["platforms"] and finds the named entries.
func platformList(item map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	var platforms []map[string]json.RawMessage
	err := json.Unmarshal(item["platforms"], &platforms)
	return platforms, err
}

func platformNamed(platforms []map[string]json.RawMessage, name string) map[string]json.RawMessage {
	for _, p := range platforms {
		var got string
		if json.Unmarshal(p["name"], &got) == nil && got == name {
			return p
		}
	}
	return nil
}

func setJSON(m map[string]json.RawMessage, key string, value any) {
	m[key], _ = json.Marshal(value)
}

// addLinuxIntegration adds the linux platform to an integration_body
// entry; false when there is nothing to base it on or it already exists.
func addLinuxIntegration(body map[string]json.RawMessage, art linuxArtifact) (bool, error) {
	platforms, err := platformList(body)
	if err != nil {
		return false, err
	}
	base := platformNamed(platforms, bundlePlatform)
	if base == nil || platformNamed(platforms, linuxPlatform) != nil {
		return false, nil
	}
	var baseArtifact struct {
		Version string `json:"version"`
	}
	json.Unmarshal(base["artifact"], &baseArtifact)
	if baseArtifact.Version != art.version {
		log.Printf("store: %s bundle is %s, Linux build pinned at %s; no Linux entry", art.appID, baseArtifact.Version, art.version)
		return false, nil
	}
	linux := maps.Clone(base)
	setJSON(linux, "name", linuxPlatform)
	setJSON(linux, "artifact", map[string]any{
		"filename": art.filename, "kind": "archive", "size": art.size, "url": art.url, "version": art.version,
	})
	setJSON(linux, "artifact_url", art.url)
	setJSON(body, "platforms", append(platforms, linux))
	return true, nil
}

// addLinuxAppPlatform adds the linux platform to the app list entry the
// store page shows (size, version, download).
func addLinuxAppPlatform(app map[string]json.RawMessage, art linuxArtifact) error {
	platforms, err := platformList(app)
	if err != nil {
		return err
	}
	if platformNamed(platforms, linuxPlatform) != nil {
		return nil
	}
	base := platformNamed(platforms, bundlePlatform)
	if base == nil && len(platforms) > 0 {
		base = platforms[0]
	}
	if base == nil {
		return fmt.Errorf("no platform to base the Linux entry on")
	}
	linux := maps.Clone(base)
	setJSON(linux, "name", linuxPlatform)
	setJSON(linux, "app_size", art.size)
	setJSON(linux, "app_version", art.version)
	setJSON(linux, "blob", art.filename)
	setJSON(linux, "blob_url", art.url)
	setJSON(linux, "sha256sum", art.sha256)
	setJSON(app, "platforms", append(platforms, linux))
	return nil
}
