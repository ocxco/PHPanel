package panel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSystemServices struct {
	services                         map[string]systemServiceStatus
	failure                          error
	starts, stops, installs, removes int
}

func fakeServices() *fakeSystemServices {
	return &fakeSystemServices{services: map[string]systemServiceStatus{}}
}
func (f *fakeSystemServices) Query(i Instance) (systemServiceStatus, error) {
	return f.services[i.ID], f.failure
}
func (f *fakeSystemServices) Install(i Instance, auto bool) error {
	if f.failure != nil {
		return f.failure
	}
	f.installs++
	f.services[i.ID] = systemServiceStatus{Exists: true, AutoStart: auto, Status: "stopped"}
	return nil
}
func (f *fakeSystemServices) SetAutoStart(i Instance, auto bool) error {
	if f.failure != nil {
		return f.failure
	}
	s := f.services[i.ID]
	s.AutoStart = auto
	f.services[i.ID] = s
	return nil
}
func (f *fakeSystemServices) Remove(i Instance) error {
	if f.failure != nil {
		return f.failure
	}
	f.removes++
	delete(f.services, i.ID)
	return nil
}
func (f *fakeSystemServices) Start(i Instance) error {
	if f.failure != nil {
		return f.failure
	}
	f.starts++
	s := f.services[i.ID]
	s.Status, s.PID = "running", 9001
	f.services[i.ID] = s
	return nil
}
func (f *fakeSystemServices) Stop(i Instance) error {
	if f.failure != nil {
		return f.failure
	}
	f.stops++
	s := f.services[i.ID]
	s.Status, s.PID = "stopped", 0
	f.services[i.ID] = s
	return nil
}

func serviceFixture(t *testing.T, component, version string) (*Manager, Instance, *fakeSystemServices) {
	t.Helper()
	m := testManager(t)
	f := fakeServices()
	m.systemServices = f
	i := addServiceFixture(t, m, component, version, 19681)
	return m, i, f
}
func addServiceFixture(t *testing.T, m *Manager, component, version string, port int) Instance {
	t.Helper()
	p, err := m.ImportArchive(component, version, writeFixture(t, testZIP(t, map[string][]byte{executableName(component): fakePE(), "version.txt": []byte(version)})), "")
	if err != nil {
		t.Fatal(err)
	}
	i, err := m.CreateInstance(p.ID, component+" "+version, port)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestSystemServiceRegistrationLifecycle(t *testing.T) {
	for _, component := range []string{"nginx", "redis", "mysql"} {
		t.Run(component, func(t *testing.T) {
			m, i, f := serviceFixture(t, component, "1.0")
			if err := m.RegisterSystemService(i.ID, true); err != nil {
				t.Fatal(err)
			}
			current, _, _ := m.lookupInstance(i.ID)
			if current.ServiceName == "" || !current.ServiceAutoStart || current.Status != "stopped" || f.starts != 0 {
				t.Fatal("registration unexpectedly started service or lost settings", current)
			}
			if err := m.RegisterSystemService(i.ID, true); err == nil {
				t.Fatal("double registration accepted")
			}
			if err := m.StartInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			current, _, _ = m.lookupInstance(i.ID)
			if current.Status != "running" || current.PID != 9001 || f.starts != 1 || len(m.processes) != 0 {
				t.Fatal("start did not delegate to SCM", current)
			}
			if err := m.UnregisterSystemService(i.ID); err == nil {
				t.Fatal("running service removed")
			}
			if err := m.StopInstance(i.ID); err != nil {
				t.Fatal(err)
			}
			if err := m.SetSystemServiceAutoStart(i.ID, false); err != nil {
				t.Fatal(err)
			}
			current, _, _ = m.lookupInstance(i.ID)
			if current.ServiceAutoStart || f.stops != 1 {
				t.Fatal("startup type or stop lost")
			}
			if err := m.UnregisterSystemService(i.ID); err != nil {
				t.Fatal(err)
			}
			current, _, _ = m.lookupInstance(i.ID)
			if current.ServiceName != "" || f.removes != 1 {
				t.Fatal("unregister left metadata")
			}
			if _, err := os.Stat(current.Path); err != nil {
				t.Fatal("unregister deleted instance")
			}
		})
	}
}

func TestSystemServiceRejectsRunningPHPAndConflictingRegistration(t *testing.T) {
	m, i, f := serviceFixture(t, "nginx", "1.0")
	if err := m.setInstance(i.ID, "running", "", 42); err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterSystemService(i.ID, true); err == nil {
		t.Fatal("running instance registered")
	}
	_ = m.setInstance(i.ID, "stopped", "", 0)
	if err := m.RegisterSystemService(i.ID, false); err != nil {
		t.Fatal(err)
	}
	other := addServiceFixture(t, m, "nginx", "2.0", 0)
	if err := m.RegisterSystemService(other.ID, true); err == nil || f.installs != 1 {
		t.Fatal("second Nginx version registered")
	}
	php := addServiceFixture(t, m, "php", "8.4.0", 19683)
	if err := m.RegisterSystemService(php.ID, true); err == nil {
		t.Fatal("PHP registered")
	}
	if err := m.StartInstance(i.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.StartInstance(other.ID); err == nil {
		t.Fatal("ordinary Nginx started beside SCM Nginx")
	}
}

func TestSystemServiceReconcilesExternalStateAndGuardsData(t *testing.T) {
	m, i, f := serviceFixture(t, "redis", "1.0")
	if err := m.RegisterSystemService(i.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(i.ID); err == nil {
		t.Fatal("registered instance deleted")
	}
	if _, err := m.UpdateInstancePort(i.ID, 19682); err != nil {
		t.Fatal("stopped registered service cannot change port", err)
	}
	f.services[i.ID] = systemServiceStatus{Exists: true, Status: "running", PID: 9002, AutoStart: true}
	if _, err := m.UpdateInstancePort(i.ID, 19684); err == nil {
		t.Fatal("externally started service edited")
	}
	current, _, _ := m.lookupInstance(i.ID)
	if current.PID != 9002 || current.Status != "running" {
		t.Fatal("external state missing")
	}
	f.failure = fmt.Errorf("access denied")
	if _, err := m.UpdateInstancePort(i.ID, 19684); err == nil {
		t.Fatal("inaccessible service edited")
	}
	if err := m.UnregisterSystemService(i.ID); err == nil {
		t.Fatal("inaccessible service unregistered")
	}
	f.failure = nil
	delete(f.services, i.ID)
	m.RefreshSystemServices()
	current, _, _ = m.lookupInstance(i.ID)
	if current.ServiceName != "" || current.Status != "stopped" {
		t.Fatal("external deletion not reconciled")
	}
}

func TestSystemServiceSurvivesManagerCloseAndRecoversLostRecord(t *testing.T) {
	m, i, f := serviceFixture(t, "redis", "1.0")
	// SCM can contain a successful registration whose metadata save was interrupted.
	f.services[i.ID] = systemServiceStatus{Exists: true, Status: "running", PID: 22, AutoStart: true}
	m.RefreshSystemServices()
	current, _, _ := m.lookupInstance(i.ID)
	if current.ServiceName == "" || current.Status != "running" {
		t.Fatal("registration not recovered")
	}
	m.Close()
	if f.stops != 0 || f.services[i.ID].Status != "running" {
		t.Fatal("GUI close stopped system service")
	}
}

func TestSystemServiceHostUsesLatestMetadataWithoutLockOrWrites(t *testing.T) {
	m, i, _ := serviceFixture(t, "redis", "1.0")
	if err := m.RegisterSystemService(i.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateInstancePort(i.ID, 19686); err != nil {
		t.Fatal(err)
	}
	i, _, _ = m.lookupInstance(i.ID)
	original, err := os.ReadFile(m.statePath())
	if err != nil {
		t.Fatal(err)
	}
	host, selected, err := loadServiceHost(m.root, i.ID, i.ServiceName)
	if err != nil {
		t.Fatal("host failed while GUI owns lock", err)
	}
	if selected.Port != 19686 || !host.serviceHost {
		t.Fatal("host read stale port")
	}
	if err := host.setInstance(i.ID, "running", "", 42); err != nil {
		t.Fatal(err)
	}
	host.Close()
	after, _ := os.ReadFile(m.statePath())
	if !bytes.Equal(original, after) {
		t.Fatal("service host modified GUI state")
	}
	if _, _, err := loadServiceHost(m.root, i.ID, "wrong-service"); err == nil {
		t.Fatal("wrong registration accepted")
	}
}

func TestSCMNginxReloadsOnSiteChanges(t *testing.T) {
	m, _, _, _, _ := siteFixture(t)
	f := fakeServices()
	m.systemServices = f
	var nginx Instance
	for _, i := range m.Snapshot().Instances {
		if i.Component == "nginx" {
			nginx = i
			break
		}
	}
	f.services[nginx.ID] = systemServiceStatus{Exists: true, Status: "running", PID: 999}
	reloads := 0
	m.nginxReloader = func(Instance, InstalledPackage) error { reloads++; return nil }
	site := m.Snapshot().Sites
	// siteFixture has no websites; create one with the normal validator fixture.
	if len(site) == 0 {
		root := filepath.Join(m.root, "project")
		_ = os.MkdirAll(root, 0700)
		if _, err := m.CreateSiteWithOptions("service-site", root, nginx.ID, "", 19690, "static"); err != nil {
			t.Fatal(err)
		}
	}
	if reloads == 0 {
		t.Fatal("SCM-owned Nginx did not reload")
	}
}

func TestSystemServiceNamesAreScopedAndStable(t *testing.T) {
	i := Instance{ID: "one", Component: "nginx"}
	name := systemServiceName(filepath.Join(t.TempDir(), "root"), i)
	if !strings.HasPrefix(name, "PHPanel-nginx-") || len(name) > 100 {
		t.Fatal(name)
	}
	i.ServiceName = name
	if systemServiceName("other-root", i) != name {
		t.Fatal("persisted name changed after move")
	}
	if systemServiceName("same", Instance{ID: "one", Component: "nginx"}) == systemServiceName("same", Instance{ID: "two", Component: "nginx"}) {
		t.Fatal("instances share service name")
	}
}
