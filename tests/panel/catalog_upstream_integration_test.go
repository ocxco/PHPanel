//go:build integration

package panel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveUpstreamDiscovery(t *testing.T) {
	if os.Getenv("PHPANEL_LIVE_CATALOG_TEST") != "1" {
		t.Skip("set PHPANEL_LIVE_CATALOG_TEST=1")
	}
	m := testManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	warnings, err := m.RefreshCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		t.Log(w)
	}
	counts := map[string]int{}
	for _, a := range m.Snapshot().Artifacts {
		counts[a.Component]++
	}
	t.Logf("live catalogue: %v", counts)
	if counts["mysql"] <= 8 || counts["nginx"] <= 2 {
		t.Fatalf("new versions not discovered: %v", counts)
	}
}
func TestOfficialDetachedSignatures(t *testing.T) {
	if os.Getenv("PHPANEL_LIVE_CATALOG_TEST") != "1" {
		t.Skip("set PHPANEL_LIVE_CATALOG_TEST=1")
	}
	root := os.Getenv("PHPANEL_SMOKE_ARCHIVES_DIR")
	if root == "" {
		t.Skip("set PHPANEL_SMOKE_ARCHIVES_DIR")
	}
	m := testManager(t)
	client := m.httpClient()
	defer client.CloseIdleConnections()
	for _, item := range []Artifact{{Component: "nginx", Version: "1.28.3", SignatureAuthority: "nginx", SignatureURL: "https://nginx.org/download/nginx-1.28.3.zip.asc"}, {Component: "mysql", Version: "8.4.8", SignatureAuthority: "mysql", SignatureURL: "https://cdn.mysql.com/Downloads/MySQL-8.4/mysql-8.4.8-winx64.zip.asc"}} {
		t.Run(item.Component, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			archive := filepath.Join(root, item.Component+"-"+item.Version+".zip")
			if err := m.verifyArtifactSignature(ctx, client, item, archive); err != nil {
				t.Fatal(err)
			}
			t.Log("official signature verified")
		})
	}
}
func TestLiveSignedPackageInstallation(t *testing.T) {
	if os.Getenv("PHPANEL_LIVE_CATALOG_TEST") != "1" {
		t.Skip("set PHPANEL_LIVE_CATALOG_TEST=1")
	}
	m := testManager(t)
	client := m.httpClient()
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	body, err := catalogueBody(ctx, client, "https://nginx.org/en/download.html", 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	items, err := nginxArtifacts(body)
	if err != nil {
		t.Fatal(err)
	}
	var selected Artifact
	for _, a := range items {
		known := false
		for _, seed := range seedCatalog() {
			if sameRelease(seed, a) {
				known = true
			}
		}
		if !known {
			selected = a
			break
		}
	}
	if selected.ID == "" {
		t.Skip("all currently published Nginx versions are already pinned")
	}
	if err := m.AddArtifact(selected); err != nil {
		t.Fatal(err)
	}
	key, err := m.Install(selected.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(80 * time.Second)
	for time.Now().Before(deadline) {
		for _, task := range m.Snapshot().Tasks {
			if task.ID != key {
				continue
			}
			if task.Status == "error" {
				t.Fatal(task.Error)
			}
			if task.Status == "completed" {
				state := m.Snapshot()
				if len(state.Packages) != 1 || state.Packages[0].ID != selected.ID {
					t.Fatal("signed install identity mismatch")
				}
				t.Logf("discovered and installed Nginx %s with official signature", selected.Version)
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("live signed package did not finish")
}
