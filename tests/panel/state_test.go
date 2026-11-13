package panel

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSnapshotIsolationAndRevision(t *testing.T) {
	m := testManager(t)
	before := m.Snapshot()
	snapshot := m.Snapshot()
	snapshot.Artifacts[0].Sources[0].URL = "https://invalid.example/changed"
	if m.Snapshot().Artifacts[0].Sources[0].URL != before.Artifacts[0].Sources[0].URL {
		t.Fatal("nested source slice leaked through snapshot")
	}
	update := m.SnapshotSince(before.Revision)
	if update.Changed || update.State != nil {
		t.Fatal("unchanged revision returned a full state")
	}
	notified := 0
	m.SetChangeHandler(func() { notified++; _ = m.Snapshot() })
	if err := m.SaveSettings(Settings{ProxyMode: "direct", SourceMode: "custom"}); err != nil {
		t.Fatal(err)
	}
	update = m.SnapshotSince(before.Revision)
	if !update.Changed || update.State == nil || update.Revision <= before.Revision || notified != 1 {
		t.Fatal("mutation did not advance revision and emit notification")
	}
	revision := update.Revision
	oldURL := m.Snapshot().Artifacts[0].Sources[0].URL
	if err := m.change(func(s *State) error { s.Artifacts[0].Sources[0].URL = "invalid"; return errors.New("rollback") }); err == nil {
		t.Fatal("failed transaction succeeded")
	}
	if m.Snapshot().Revision != revision || m.Snapshot().Artifacts[0].Sources[0].URL != oldURL || notified != 1 {
		t.Fatal("rollback changed revision, source or notification")
	}
}

func BenchmarkStateCopy(b *testing.B) {
	state := State{Revision: 1, Artifacts: seedCatalog(), Packages: []InstalledPackage{}, Instances: []Instance{}, Sites: []Site{}, Tasks: []Task{}}
	b.Run("json-roundtrip", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			data, err := json.Marshal(state)
			if err != nil {
				b.Fatal(err)
			}
			var snapshot State
			if err := json.Unmarshal(data, &snapshot); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("slice-clone", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			snapshot := cloneState(state)
			if len(snapshot.Artifacts) != len(state.Artifacts) {
				b.Fatal("invalid snapshot")
			}
		}
	})
}
func TestPackageRemovalNotifiesStateChange(t *testing.T) {
	m := testManager(t)
	p, err := m.ImportArchive("nginx", "test", writeFixture(t, testZIP(t, map[string][]byte{"nginx.exe": fakePE()})), "")
	if err != nil {
		t.Fatal(err)
	}
	before := m.Snapshot().Revision
	notified := false
	m.SetChangeHandler(func() { notified = true; _ = m.Snapshot() })
	if err := m.RemovePackage(p.ID); err != nil {
		t.Fatal(err)
	}
	update := m.SnapshotSince(before)
	if !notified || !update.Changed || update.State == nil || len(update.State.Packages) != 0 {
		t.Fatal("removal did not reach subscribers")
	}
}
