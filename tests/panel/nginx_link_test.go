package panel

import (
	"fmt"
	"testing"
)

type fakePHPRuntime struct {
	status        map[string]phpRuntimeStatus
	starts, stops []string
	failID        string
}

func (f *fakePHPRuntime) Query(i Instance) (phpRuntimeStatus, error) {
	s := f.status[i.ID]
	s.Persistent = s.Exists
	return s, nil
}
func (f *fakePHPRuntime) Start(i Instance, owner, run string) (phpRuntimeStatus, error) {
	if i.ID == f.failID {
		return phpRuntimeStatus{}, fmt.Errorf("fixture PHP failure")
	}
	f.starts = append(f.starts, i.ID)
	s := phpRuntimeStatus{Exists: true, Status: "running", PID: 9191, Owner: owner, Run: run}
	f.status[i.ID] = s
	return s, nil
}
func (f *fakePHPRuntime) Stop(i Instance, run string) error {
	if run == "close-unprotected" {
		if f.status[i.ID].Protected {
			return nil
		}
		run = ""
	}
	if run != "" && f.status[i.ID].Run != run {
		return fmt.Errorf("wrong run")
	}
	f.stops = append(f.stops, i.ID)
	delete(f.status, i.ID)
	return nil
}
func (*fakePHPRuntime) Diagnostic(Instance) (PHPProbe, error) { return PHPProbe{}, nil }
func (f *fakePHPRuntime) Protect(i Instance, enabled bool) error {
	s := f.status[i.ID]
	s.Protected = enabled
	f.status[i.ID] = s
	return nil
}

func linkedFixture(t *testing.T) (*Manager, Instance, Instance, Instance, *fakePHPRuntime) {
	t.Helper()
	m, n, _ := serviceFixture(t, "nginx", "fixture")
	a := addServiceFixture(t, m, "php", "8.3", 19811)
	b := addServiceFixture(t, m, "php", "8.4", 19812)
	f := &fakePHPRuntime{status: map[string]phpRuntimeStatus{}}
	m.phpRuntime = f
	m.serviceHost = true
	m.linkedNginxStarter = func(key string, lease *runtimeLease) error {
		lease.release()
		return m.setInstance(key, "running", "", 9393)
	}
	return m, n, a, b, f
}
func addLinkedSites(t *testing.T, m *Manager, ids ...string) {
	t.Helper()
	err := m.change(func(s *State) error {
		for index, key := range ids {
			s.Sites = append(s.Sites, Site{ID: fmt.Sprintf("site-%d", index), Template: "php", PHPID: key})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNginxDependenciesDeduplicateSharedSitesAndRejectSamePackage(t *testing.T) {
	m, _, a, b, _ := linkedFixture(t)
	addLinkedSites(t, m, a.ID, a.ID, b.ID)
	plan, err := nginxPHPDependencies(m.Snapshot())
	if err != nil || len(plan) != 2 {
		t.Fatalf("plan %v: %v", plan, err)
	}
	state := m.Snapshot()
	state.Sites = append(state.Sites, Site{Template: "static", PHPID: "missing"})
	if _, err := nginxPHPDependencies(state); err != nil {
		t.Fatal("static site dependency", err)
	}
	state.Instances = append(state.Instances, Instance{ID: "duplicate", Component: "php", PackageID: a.PackageID, Name: "same package"})
	state.Sites = append(state.Sites, Site{Template: "php", PHPID: "duplicate"})
	if _, err := nginxPHPDependencies(state); err == nil {
		t.Fatal("same PHP package instances accepted")
	}
	state.Sites = []Site{{Template: "php", PHPID: "deleted"}}
	if _, err := nginxPHPDependencies(state); err == nil {
		t.Fatal("missing PHP accepted")
	}
}
func TestNginxLinkedStartReusesExistingAndCombinedStopOnlyOwnsNewPHP(t *testing.T) {
	m, n, a, b, f := linkedFixture(t)
	addLinkedSites(t, m, a.ID, a.ID, b.ID)
	f.status[a.ID] = phpRuntimeStatus{Exists: true, Status: "running", PID: 9001}
	if err := m.startNginxLinked(n.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.starts) != 1 || f.starts[0] != b.ID {
		t.Fatal("did not reuse existing PHP", f.starts)
	}
	j, err := m.readLinkJournal(n.ID)
	if err != nil || len(j.PHPIDs) != 1 || j.PHPIDs[0] != b.ID {
		t.Fatal(j, err)
	}
	if err := m.StopInstance(n.ID); err != nil {
		t.Fatal(err)
	}
	if !f.status[b.ID].Exists {
		t.Fatal("default Nginx stop killed linked PHP")
	}
	if err := m.StopNginxWithPHP(n.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.stops) != 1 || f.stops[0] != b.ID || !f.status[a.ID].Exists {
		t.Fatal("combined stop killed reused PHP", f.stops)
	}
}
func TestNginxFailureRollsBackOnlyThisRun(t *testing.T) {
	m, n, a, b, f := linkedFixture(t)
	addLinkedSites(t, m, a.ID, b.ID)
	f.status[a.ID] = phpRuntimeStatus{Exists: true, Status: "running", PID: 9001}
	m.linkedNginxStarter = func(string, *runtimeLease) error { return fmt.Errorf("bad Nginx config") }
	if err := m.startNginxLinked(n.ID); err == nil {
		t.Fatal("Nginx failure ignored")
	}
	if f.status[b.ID].Exists || !f.status[a.ID].Exists || len(f.stops) != 1 {
		t.Fatal("rollback ownership failed")
	}
	current, _, _ := m.lookupInstance(n.ID)
	if current.Status != "error" {
		t.Fatal(current)
	}
}
func TestNginxPHPFailureStopsNginxStartupAndRetainsExistingPHP(t *testing.T) {
	m, n, a, b, f := linkedFixture(t)
	addLinkedSites(t, m, a.ID, b.ID)
	f.status[a.ID] = phpRuntimeStatus{Exists: true, Status: "running", PID: 9001}
	f.failID = b.ID
	nginxStarted := false
	m.linkedNginxStarter = func(string, *runtimeLease) error { nginxStarted = true; return nil }
	if err := m.startNginxLinked(n.ID); err == nil {
		t.Fatal("failed PHP ignored")
	}
	if nginxStarted || !f.status[a.ID].Exists {
		t.Fatal("Nginx started before dependency ready")
	}
}
func TestNginxLinkOptionsAndRunIdentity(t *testing.T) {
	m, n, a, _, f := linkedFixture(t)
	addLinkedSites(t, m, a.ID)
	if !nginxAutoPHP(n) || n.NginxStopPHP {
		t.Fatal("wrong defaults")
	}
	if err := m.SaveNginxLinkOptions(n.ID, false, false); err != nil {
		t.Fatal(err)
	}
	if err := m.startNginxLinked(n.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.starts) != 0 {
		t.Fatal("disabled linkage started PHP")
	}
	if err := m.SaveNginxLinkOptions(n.ID, true, true); err == nil {
		t.Fatal("running options changed")
	}
	_ = m.StopInstance(n.ID)
	if err := m.SaveNginxLinkOptions(n.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if err := m.startNginxLinked(n.ID); err != nil {
		t.Fatal(err)
	}
	j, _ := m.readLinkJournal(n.ID)
	// A stopped/restarted PHP is no longer owned by the old linkage generation.
	f.status[a.ID] = phpRuntimeStatus{Exists: true, Status: "running", PID: 9002, Owner: n.ID, Run: "another-run"}
	if err := m.stopLinkJournal(j); err != nil {
		t.Fatal(err)
	}
	if !f.status[a.ID].Exists {
		t.Fatal("stale linkage stopped replacement PHP")
	}
}
func TestNginxOptionSnapshotDoesNotExposeMutableState(t *testing.T) {
	m, n, _, _, _ := linkedFixture(t)
	if err := m.SaveNginxLinkOptions(n.ID, false, false); err != nil {
		t.Fatal(err)
	}
	snapshot := m.Snapshot()
	for _, i := range snapshot.Instances {
		if i.ID == n.ID {
			*i.NginxAutoPHP = true
		}
	}
	actual, _, _ := m.lookupInstance(n.ID)
	if nginxAutoPHP(actual) {
		t.Fatal("snapshot modified stored options")
	}
}

func TestDetachedPHPExitStopsOnlyUnprotectedOwnedRuntimes(t *testing.T) {
	m, _, a, b, f := linkedFixture(t)
	m.serviceHost = false
	m.ownedDetachedPHP = map[string]bool{a.ID: true, b.ID: true}
	f.status[a.ID] = phpRuntimeStatus{Exists: true, Status: "running", PID: 11}
	f.status[b.ID] = phpRuntimeStatus{Exists: true, Status: "running", PID: 12, Protected: true}
	m.Close()
	if f.status[a.ID].Exists || !f.status[b.ID].Exists {
		t.Fatal("GUI exit ignored protected PHP")
	}
}
