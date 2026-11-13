package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

func TestFutureMySQLAndNginxVersionsAreDiscovered(t *testing.T) {
	mysqlBody := []byte(`<select id="version"><option value="97.4">97.4.12 LTS</option><option value="98.0">98.0.1</option></select><td>(mysql-97.4.12-winx64.zip)</td><td>(mysql-97.4.12-winx64-debug-test.zip)</td>`)
	branches := mysqlBranches(mysqlBody)
	if len(branches) != 2 || branches[0] != "97.4" {
		t.Fatal(branches)
	}
	items, err := mysqlArtifacts(mysqlBody)
	if err != nil || len(items) != 1 || items[0].Version != "97.4.12" || !validSignatureMetadata(items[0]) {
		t.Fatalf("MySQL discovery: %+v %v", items, err)
	}
	items, err = nginxArtifacts([]byte(`<a href="../download/nginx-98.12.7.zip">Windows</a>`))
	if err != nil || len(items) != 1 || items[0].Version != "98.12.7" || items[0].SHA256 != "" {
		t.Fatalf("Nginx discovery: %+v %v", items, err)
	}
	forged := items[0]
	forged.SignatureURL = "https://attacker.invalid/nginx-98.12.7.zip.asc"
	if validateArtifact(forged) == nil {
		t.Fatal("untrusted signature metadata accepted")
	}
}
func TestRemoteCatalogUpdatesWithoutCodeChanges(t *testing.T) {
	item := Artifact{Component: "php", Version: "98.2.3", SHA256: strings.Repeat("a", 64), Sources: []Source{{Name: "fixture", URL: "https://example.invalid/php.zip"}}}
	body, _ := json.Marshal(map[string]any{"schemaVersion": 1, "artifacts": []Artifact{item}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	m := testManager(t)
	provider := catalogProvider{name: "fixture", fetch: func(ctx context.Context, client *http.Client) ([]Artifact, error) {
		data, err := catalogueBody(ctx, client, server.URL, 4<<20)
		if err != nil {
			return nil, err
		}
		return remoteArtifacts(data)
	}}
	if _, err := m.refreshWithProviders(context.Background(), []catalogProvider{provider}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range m.Snapshot().Artifacts {
		if a.Version == "98.2.3" {
			found = true
		}
	}
	if !found {
		t.Fatal("remote future version not merged")
	}
	previous := len(m.Snapshot().Artifacts)
	broken := catalogProvider{name: "offline", fetch: func(context.Context, *http.Client) ([]Artifact, error) { return nil, fmt.Errorf("offline") }}
	warnings, err := m.refreshWithProviders(context.Background(), []catalogProvider{broken})
	if err != nil || len(warnings) != 1 || len(m.Snapshot().Artifacts) != previous {
		t.Fatal("failed source removed cached catalogue")
	}
	item.SHA256 = ""
	body, _ = json.Marshal(map[string]any{"schemaVersion": 1, "artifacts": []Artifact{item}})
	if _, err := remoteArtifacts(body); err == nil {
		t.Fatal("unsigned unverified remote package accepted")
	}
}
func TestLiveMergePreservesPinnedDigestAndUserMirrors(t *testing.T) {
	incoming := mysqlVersionArtifact("98.1.2", "fixture")
	pinned := incoming
	pinned.ID = "mysql-" + strings.Repeat("b", 24)
	pinned.SHA256 = strings.Repeat("b", 64)
	pinned.SignatureURL = ""
	pinned.SignatureAuthority = ""
	pinned.Sources = []Source{{Name: "自定义镜像", URL: "https://example.invalid/mysql-98.1.2-winx64.zip"}}
	state := State{Artifacts: []Artifact{pinned}}
	if added := mergeLiveCatalog(&state, []Artifact{incoming}); added != 0 || len(state.Artifacts) != 1 {
		t.Fatal("same official ZIP created duplicate")
	}
	if state.Artifacts[0].SHA256 != pinned.SHA256 || state.Artifacts[0].Sources[0].Name != "自定义镜像" || state.Artifacts[0].ID != pinned.ID {
		t.Fatal("pinned digest/source identity overwritten")
	}
}
func TestUpdaterCancellationAndInterval(t *testing.T) {
	settings := Settings{CatalogAutoUpdate: true, CatalogUpdateHours: 6}
	now := time.Now()
	status := CatalogStatus{LastAttempt: now.Add(-time.Hour).Format(time.RFC3339)}
	if catalogDue(settings, status, now) {
		t.Fatal("fresh catalogue unnecessarily refreshed")
	}
	status.LastAttempt = now.Add(-7 * time.Hour).Format(time.RFC3339)
	if !catalogDue(settings, status, now) {
		t.Fatal("stale catalogue not refreshed")
	}
	m := testManager(t)
	started := make(chan struct{})
	finished := make(chan struct{})
	provider := catalogProvider{name: "blocking", fetch: func(ctx context.Context, _ *http.Client) ([]Artifact, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	go func() {
		_, _ = m.refreshWithProviders(context.Background(), []catalogProvider{provider})
		close(finished)
	}()
	<-started
	m.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("refresh outlived manager close")
	}
	if m.Snapshot().Catalog.Refreshing {
		t.Fatal("refresh flag stuck after cancellation")
	}
}
func TestSignedDownloadVerifiesBeforePublishingAndKeepsIdentity(t *testing.T) {
	signer, err := openpgp.NewEntity("fixture", "", "fixture@example.invalid", &packet.Config{RSABits: 2048})
	if err != nil {
		t.Fatal(err)
	}
	payload := testZIP(t, map[string][]byte{"nginx.exe": fakePE()})
	var signature bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&signature, signer, bytes.NewReader(payload), nil); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			m := testManager(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if corrupt {
					_, _ = w.Write(append(append([]byte{}, payload...), []byte("tampered")...))
				} else {
					_, _ = w.Write(payload)
				}
			}))
			defer server.Close()
			item := Artifact{Component: "nginx", Version: "98.1.2", SignatureAuthority: "nginx", SignatureURL: "https://nginx.org/download/nginx-98.1.2.zip.asc", Sources: []Source{{Name: "fixture", URL: server.URL}}}
			item.ID = artifactID(item)
			m.signatureVerifier = func(ctx context.Context, _ *http.Client, _ Artifact, archive string) error {
				return checkSignature(ctx, openpgp.EntityList{signer}, archive, signature.Bytes())
			}
			if err := m.AddArtifact(item); err != nil {
				t.Fatal(err)
			}
			key, err := m.Install(item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				waitTask(t, m, key, "error")
				if len(m.Snapshot().Packages) != 0 {
					t.Fatal("invalid signature installed")
				}
				return
			}
			waitTask(t, m, key, "completed")
			state := m.Snapshot()
			if len(state.Packages) != 1 || state.Packages[0].ID != item.ID {
				t.Fatal("signature promotion changed package identity")
			}
			found := false
			for _, a := range state.Artifacts {
				if a.ID == item.ID {
					found = a.SHA256 == digest(payload)
				}
			}
			if !found {
				t.Fatal("verified digest not cached")
			}
		})
	}
}
func TestAutomaticSettingsMigrateFromOlderState(t *testing.T) {
	m := testManager(t)
	root := m.root
	m.Close()
	body, err := os.ReadFile(m.statePath())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	_ = json.Unmarshal(body, &document)
	settings := document["settings"].(map[string]any)
	delete(settings, "catalogAutoUpdate")
	delete(settings, "catalogUpdateHours")
	delete(settings, "catalogUrls")
	body, _ = json.Marshal(document)
	if err := os.WriteFile(m.statePath(), body, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if !again.Snapshot().Settings.CatalogAutoUpdate || again.Snapshot().Settings.CatalogUpdateHours != 6 {
		t.Fatal("old settings did not get auto-update defaults")
	}
}
