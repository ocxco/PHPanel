package panel

import "testing"

func TestLookupInstanceSynthesizesPHPPackage(t *testing.T) {
	m := testManager(t)
	pkg := InstalledPackage{
		ID:         "php-test-merge",
		Component:  "php",
		Version:    "8.5.0",
		Path:       `C:\pkg\package-php8.5`,
		Executable: `C:\pkg\package-php8.5\php.exe`,
		PHPRuntime: PHPRuntime{Port: 9085, Status: "stopped"},
	}
	if err := m.change(func(s *State) error {
		s.Packages = append(s.Packages, pkg)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	i, p, err := m.lookupInstance("php-test-merge")
	if err != nil {
		t.Fatal(err)
	}
	// TR-3.1: synthesized Instance carries package identity & runtime fields.
	if i.ID != "php-test-merge" || i.Component != "php" || i.Path != pkg.Path || i.Port != 9085 || i.PackageID != pkg.ID {
		t.Fatalf("synthesized instance mismatch: %+v", i)
	}
	if p.ID != "php-test-merge" {
		t.Fatalf("returned package mismatch: %+v", p)
	}

	// TR-3.2: setInstance writes back to the package runtime and creates no Instance row.
	if err := m.setInstance("php-test-merge", "running", "", 4321); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	if len(snap.Instances) != 0 {
		t.Fatalf("expected no instance rows for PHP, got %d", len(snap.Instances))
	}
	found := false
	for _, pp := range snap.Packages {
		if pp.ID == "php-test-merge" {
			found = true
			if pp.Status != "running" || pp.PID != 4321 {
				t.Fatalf("package runtime not updated: status=%q pid=%d", pp.Status, pp.PID)
			}
		}
	}
	if !found {
		t.Fatal("package not found in snapshot")
	}
}
