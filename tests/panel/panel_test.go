package panel

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(filepath.Join(t.TempDir(), "work space"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if err := m.SaveSettings(Settings{ProxyMode: "direct", SourceMode: "official"}); err != nil {
		t.Fatal(err)
	}
	return m
}
func testZIP(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, data := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func fakePE() []byte {
	b := make([]byte, 512)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x84:], 0x8664)
	return b
}
func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func writeFixture(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func waitTask(t *testing.T, m *Manager, key string, status string) Task {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for _, task := range m.Snapshot().Tasks {
			if task.ID == key && (task.Status == status || task.Status == "error") {
				if task.Status != status {
					t.Fatalf("task: %+v", task)
				}
				return task
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task did not reach %s", status)
	return Task{}
}

func TestArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../outside.txt", "a/../../outside.txt", "C:/outside.txt", "a\\..\\outside.txt", "/absolute.txt", "CON.txt", "folder./file.txt", "file.txt:stream"} {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t, testZIP(t, map[string][]byte{name: []byte("unsafe")}))
			if err := extractZIP(path, t.TempDir()); err == nil {
				t.Fatal("unsafe ZIP accepted")
			}
		})
	}
}
func TestWindowsDataRootExclusivity(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows lock test")
	}
	m := testManager(t)
	if other, err := New(m.root); err == nil {
		other.Close()
		t.Fatal("second manager acquired the same data directory")
	}
	m.Close()
	again, err := New(m.root)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}
func TestUnicodeNativeDirectoryIsRejectedClearly(t *testing.T) {
	m, err := New(filepath.Join(t.TempDir(), "中文路径"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	p, err := m.ImportArchive("nginx", "test", writeFixture(t, testZIP(t, map[string][]byte{"nginx.exe": fakePE()})), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateInstance(p.ID, "test", 19991); err == nil || !strings.Contains(err.Error(), "PHPANEL_DATA_DIR") {
		t.Fatalf("expected explicit path compatibility error, got %v", err)
	}
}
func TestArchiveRejectsInvalidExecutableAndChecksum(t *testing.T) {
	m := testManager(t)
	path := writeFixture(t, testZIP(t, map[string][]byte{"nginx.exe": []byte("not a PE executable")}))
	if _, err := m.ImportArchive("nginx", "1", path, ""); err == nil {
		t.Fatal("invalid executable accepted")
	}
	valid := writeFixture(t, testZIP(t, map[string][]byte{"nginx.exe": fakePE()}))
	if _, err := m.ImportArchive("nginx", "1", valid, strings.Repeat("0", 64)); err == nil {
		t.Fatal("bad digest accepted")
	}
	if len(m.Snapshot().Packages) != 0 {
		t.Fatal("failed installation published metadata")
	}
}
func TestPackageInstancesAndRestartPreserveData(t *testing.T) {
	m := testManager(t)
	path := writeFixture(t, testZIP(t, map[string][]byte{"bundle/php-cgi.exe": fakePE()}))
	p, err := m.ImportArchive("php", "1.0", path, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.CreateInstance(p.ID, "first", 18801)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateInstance(p.ID, "second", 18802)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == second.Path {
		t.Fatal("instance directories overlap")
	}
	dataPath := filepath.Join(first.Path, "data", "keep.txt")
	if err := os.WriteFile(dataPath, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.RemovePackage(p.ID); err == nil {
		t.Fatal("referenced package removed")
	}
	if _, err := m.CreateInstance(p.ID, "same-port", 18801); err == nil {
		t.Fatal("duplicate instance port accepted")
	}
	m.Close()
	again, err := New(m.root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if len(again.Snapshot().Instances) != 2 {
		t.Fatal("metadata did not survive restart")
	}
	b, err := os.ReadFile(dataPath)
	if err != nil || string(b) != "keep" {
		t.Fatal("data did not survive restart")
	}
}
func TestDownloaderResumesAndRestartsIgnoredRange(t *testing.T) {
	for _, ignoreRange := range []bool{false, true} {
		t.Run(fmt.Sprint(ignoreRange), func(t *testing.T) {
			m := testManager(t)
			payload := bytes.Repeat([]byte("0123456789"), 100)
			var rangeSeen atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", "\"fixed\"")
				if r.Header.Get("Range") == "bytes=100-" {
					rangeSeen.Store(true)
					if !ignoreRange {
						w.Header().Set("Content-Range", fmt.Sprintf("bytes 100-%d/%d", len(payload)-1, len(payload)))
						w.WriteHeader(206)
						_, _ = w.Write(payload[100:])
						return
					}
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			source := Source{Name: "local", URL: server.URL}
			task := Task{ID: id("task"), SourceURL: source.URL, ETag: "\"fixed\""}
			if err := m.change(func(s *State) error { s.Tasks = append(s.Tasks, task); return nil }); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(m.root, "downloads", "partial", task.ID+".part")
			_ = os.WriteFile(path, payload[:100], 0600)
			if err := m.fetch(context.Background(), server.Client(), task.ID, source, path); err != nil {
				t.Fatal(err)
			}
			actual, _ := os.ReadFile(path)
			if !bytes.Equal(actual, payload) || !rangeSeen.Load() {
				t.Fatal("resume corrupted file or did not send Range")
			}
		})
	}
}
func TestDownloaderRejectsWrongContentRange(t *testing.T) {
	m := testManager(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-9/100")
		w.WriteHeader(206)
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer server.Close()
	task := Task{ID: id("task"), SourceURL: server.URL}
	_ = m.change(func(s *State) error { s.Tasks = append(s.Tasks, task); return nil })
	path := filepath.Join(m.root, "downloads", "partial", task.ID+".part")
	_ = os.WriteFile(path, []byte("existing"), 0600)
	if err := m.fetch(context.Background(), server.Client(), task.ID, Source{Name: "local", URL: server.URL}, path); err == nil {
		t.Fatal("wrong range accepted")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "existing" {
		t.Fatal("bad response mutated partial file")
	}
}
func TestDownloadFallbackAndChecksumEnforcement(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			m := testManager(t)
			payload := testZIP(t, map[string][]byte{"nginx.exe": fakePE()})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/missing" {
					w.WriteHeader(404)
					return
				}
				if corrupt {
					_, _ = w.Write([]byte("corrupt"))
				} else {
					_, _ = w.Write(payload)
				}
			}))
			defer server.Close()
			a := Artifact{Component: "nginx", Version: "test", SHA256: digest(payload), Sources: []Source{{Name: "missing", URL: server.URL + "/missing"}, {Name: "fallback", URL: server.URL + "/package"}}}
			if err := m.AddArtifact(a); err != nil {
				t.Fatal(err)
			}
			key, err := m.Install(artifactID(a))
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				task := waitTask(t, m, key, "error")
				if !strings.Contains(task.Error, "SHA-256") {
					t.Fatalf("missing integrity error: %s", task.Error)
				}
				if len(m.Snapshot().Packages) != 0 {
					t.Fatal("corrupt download installed")
				}
			} else {
				task := waitTask(t, m, key, "completed")
				if task.Source != "fallback" || len(m.Snapshot().Packages) != 1 {
					t.Fatal("fallback not used")
				}
			}
		})
	}
}
func TestPauseResumeDownload(t *testing.T) {
	m := testManager(t)
	payload := testZIP(t, map[string][]byte{"nginx.exe": fakePE(), "large.bin": bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 20000)})
	started := make(chan struct{}, 1)
	var resumed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			var start int
			_, _ = fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &start)
			resumed.Store(true)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
			w.WriteHeader(206)
			_, _ = w.Write(payload[start:])
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload[:40])
		w.(http.Flusher).Flush()
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	a := Artifact{Component: "nginx", Version: "pause", SHA256: digest(payload), Sources: []Source{{Name: "local", URL: server.URL}}}
	if err := m.AddArtifact(a); err != nil {
		t.Fatal(err)
	}
	key, err := m.Install(artifactID(a))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	partialPath := filepath.Join(m.root, "downloads", "partial", key+".part")
	deadline := time.Now().Add(3 * time.Second)
	for {
		info, err := os.Stat(partialPath)
		if err == nil && info.Size() >= 40 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial data did not reach disk")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.PauseDownload(key); err != nil {
		t.Fatal(err)
	}
	waitTask(t, m, key, "paused")
	time.Sleep(20 * time.Millisecond)
	if err := m.ResumeDownload(key); err != nil {
		t.Fatal(err)
	}
	waitTask(t, m, key, "completed")
	if !resumed.Load() {
		t.Fatal("paused download did not resume")
	}
}
