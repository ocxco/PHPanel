package panel

import "testing"

func TestSystemSettingsDefaultsValidationAndNetworkIsolation(t *testing.T) {
	m := testManager(t)
	if m.Snapshot().Settings.System.CloseBehavior != "quit" || m.Snapshot().Settings.System.CloseBehaviorConfirmed {
		t.Fatal("first-close choice must start unconfirmed")
	}
	settings := SystemSettings{AutoStart: true, StartMinimized: true, CloseBehavior: "tray", CloseBehaviorConfirmed: true}
	if err := m.SaveSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveSettings(Settings{ProxyMode: "direct", SourceMode: "official"}); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Settings.System != settings {
		t.Fatal("network save overwrote system preferences")
	}
	if err := m.SaveTheme("dark"); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Settings.System != settings {
		t.Fatal("theme save overwrote system preferences")
	}
	if err := m.SaveSystemSettings(SystemSettings{CloseBehavior: "invalid"}); err == nil || m.Snapshot().Settings.System != settings {
		t.Fatal("invalid system setting committed")
	}
	root := m.Snapshot().Root
	m.Close()
	reopened, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Snapshot().Settings.System != settings {
		t.Fatal("confirmed close choice was lost after restart")
	}
}
