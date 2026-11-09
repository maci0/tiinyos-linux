package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const storeDoc = `{"categories":["Develop"],"app":[
 {"app_id":"opencode","app_name":"opencode","platforms":[{"name":"darwin-arm64","app_type":"cli","app_version":"v1.18.20","blob_url":"https://example/mac.zip","sha256sum":"aa"}]},
 {"app_id":"wa-n8n","platforms":[{"name":"web"}]}],
"integration_body":[
 {"app_id":"opencode","platforms":[{"name":"darwin-arm64","app_type":"cli","artifact":{"version":"v1.18.35","url":"https://example/mac-artifact.zip"},"integration":{"bundle_url":"https://example/bundle.zip","engine":"script-bundle"}}]},
 {"app_id":"wa-n8n","platforms":[{"name":"web"}]}]}`

type storePlatform struct {
	Name        string            `json:"name"`
	BlobURL     string            `json:"blob_url"`
	SHA256      string            `json:"sha256sum"`
	Artifact    map[string]any    `json:"artifact"`
	Integration map[string]string `json:"integration"`
}

type storeEntry struct {
	AppID     string          `json:"app_id"`
	Platforms []storePlatform `json:"platforms"`
}

func decodeStore(t *testing.T, raw []byte) (apps, bodies []storeEntry, other map[string]json.RawMessage) {
	t.Helper()
	var doc struct {
		App             []storeEntry `json:"app"`
		IntegrationBody []storeEntry `json:"integration_body"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw, &other)
	return doc.App, doc.IntegrationBody, other
}

func TestLinuxIntegrationAdded(t *testing.T) {
	out, added, err := withLinuxIntegrations([]byte(storeDoc))
	if err != nil || strings.Join(added, ",") != "opencode" {
		t.Fatalf("added %v, err %v", added, err)
	}
	apps, bodies, other := decodeStore(t, out)
	if string(other["categories"]) != `["Develop"]` {
		t.Fatalf("unknown field lost: %s", other["categories"])
	}
	art := linuxArtifacts[0]
	app := apps[0].Platforms
	if len(app) != 2 || app[1].Name != linuxPlatform || app[1].BlobURL != art.url || app[1].SHA256 != art.sha256 {
		t.Fatalf("app platforms %+v", app)
	}
	body := bodies[0].Platforms
	if len(body) != 2 || body[1].Name != linuxPlatform || body[1].Artifact["url"] != art.url ||
		body[1].Artifact["size"] != float64(art.size) || body[1].Integration["bundle_url"] != "https://example/bundle.zip" {
		t.Fatalf("integration platforms %+v", body)
	}
	if len(apps[1].Platforms) != 1 || len(bodies[1].Platforms) != 1 {
		t.Fatal("an app without a Linux build changed")
	}
	again, added, _ := withLinuxIntegrations(out)
	if len(added) != 0 || string(again) != string(out) {
		t.Fatal("second pass changed the document")
	}
}

func TestLinuxIntegrationSkipsOtherVersion(t *testing.T) {
	doc := strings.Replace(storeDoc, `"version":"v1.18.35"`, `"version":"v1.19.0"`, 1)
	out, added, err := withLinuxIntegrations([]byte(doc))
	if err != nil || len(added) != 0 {
		t.Fatalf("added %v, err %v", added, err)
	}
	apps, bodies, _ := decodeStore(t, out)
	if len(apps[0].Platforms) != 1 || len(bodies[0].Platforms) != 1 {
		t.Fatal("Linux entry added for a bundle of another version")
	}
}

func TestStoreMetadataPatchedOnlyWhenPlain(t *testing.T) {
	resp := func(encoding string) *http.Response {
		r := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(storeDoc))}
		if encoding != "" {
			r.Header.Set("Content-Encoding", encoding)
		}
		return r
	}
	plain := resp("")
	if err := patchStoreMetadata(plain); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(plain.Body)
	if !strings.Contains(string(body), linuxPlatform) || plain.ContentLength != int64(len(body)) {
		t.Fatalf("plain body not patched (%d bytes, length %d)", len(body), plain.ContentLength)
	}
	gz := resp("gzip")
	patchStoreMetadata(gz)
	body, _ = io.ReadAll(gz.Body)
	if string(body) != storeDoc {
		t.Fatal("encoded body changed")
	}
	bad := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("not json"))}
	patchStoreMetadata(bad)
	body, _ = io.ReadAll(bad.Body)
	if string(body) != "not json" {
		t.Fatal("unparseable body changed")
	}
}
