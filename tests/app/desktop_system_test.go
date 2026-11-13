package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"
	"phpanel/internal/panel"
)

type fakeStartupRegistry struct {
	entry      autorunEntry
	writeError error
	writes     int
}

func (r *fakeStartupRegistry) Read() (autorunEntry, error) { return r.entry, nil }
func (r *fakeStartupRegistry) Write(entry autorunEntry) error {
	if r.writeError != nil {
		return r.writeError
	}
	r.entry = entry
	r.writes++
	return nil
}

type fakeTray struct {
	ready  bool
	closed bool
}

func (t *fakeTray) Ready() bool { return t.ready }
func (t *fakeTray) Close()      { t.closed = true }

func TestDesktopCloseBehaviorAndExplicitExit(t *testing.T) {
	hides, quits := 0, 0
	d := desktopController{tray: &fakeTray{ready: true}, hide: func() { hides++ }, quit: func() { quits++ }}
	if d.beforeClose(panel.SystemSettings{CloseBehavior: "quit"}) || hides != 0 {
		t.Fatal("quit behavior hid window")
	}
	if !d.beforeClose(panel.SystemSettings{CloseBehavior: "tray"}) || hides != 1 || quits != 0 {
		t.Fatal("tray behavior quit app")
	}
	d.exit()
	if quits != 1 || d.beforeClose(panel.SystemSettings{CloseBehavior: "tray"}) {
		t.Fatal("explicit tray exit was intercepted")
	}
	d.forceExit.Store(false)
	d.tray = &fakeTray{ready: false}
	if d.beforeClose(panel.SystemSettings{CloseBehavior: "tray"}) {
		t.Fatal("unavailable tray stranded hidden window")
	}
}
func TestSystemPreferenceSaveRollsBackStartupAndRetainsForeignEntry(t *testing.T) {
	old := autorunEntry{Exists: true, Value: "previous app", Kind: 2}
	r := &fakeStartupRegistry{entry: old}
	d := desktopController{registry: r, tray: &fakeTray{ready: true}}
	settings := panel.SystemSettings{AutoStart: true, StartMinimized: true, CloseBehavior: "tray"}
	if err := d.save(settings, "new command", func(panel.SystemSettings) error { return errors.New("save failed") }); err == nil || r.entry != old {
		t.Fatal("failed save changed autorun", r.entry, err)
	}
	r.writeError = errors.New("registry denied")
	persisted := false
	if err := d.save(settings, "new command", func(panel.SystemSettings) error { persisted = true; return nil }); err == nil || persisted {
		t.Fatal("registry failure committed preferences")
	}
	r.writeError = nil
	if err := d.save(settings, "new command", func(actual panel.SystemSettings) error {
		if actual != settings {
			t.Fatal(actual)
		}
		return nil
	}); err != nil || r.entry.Value != "new command" {
		t.Fatal(err, r.entry)
	}
	settings.AutoStart = false
	if err := d.save(settings, "new command", func(panel.SystemSettings) error { return nil }); err != nil || r.entry.Exists {
		t.Fatal("disable did not remove owned entry", err)
	}
	r.entry = old
	if err := d.save(settings, "different command", func(panel.SystemSettings) error { return nil }); err != nil || r.entry != old {
		t.Fatal("disable removed another installation")
	}
	d.tray = &fakeTray{ready: false}
	if err := d.save(settings, "new command", func(panel.SystemSettings) error { return nil }); err == nil {
		t.Fatal("unavailable tray setting accepted")
	}
}
func TestDesktopLaunchAndStartupState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "space 中文")
	value, err := parseDesktopLaunch([]string{startupArgument, dataRootArgument + root})
	if err != nil || value != root {
		t.Fatal(value, err)
	}
	if _, err := parseDesktopLaunch([]string{dataRootArgument + root, dataRootArgument + root}); err == nil {
		t.Fatal("duplicate roots accepted")
	}
	if _, err := parseDesktopLaunch([]string{dataRootArgument + "relative"}); err == nil {
		t.Fatal("relative root accepted")
	}
	if state := desktopStartState(panel.SystemSettings{StartMinimized: true}, false); state != options.Minimised {
		t.Fatal(state)
	}
	if state := desktopStartState(panel.SystemSettings{StartMinimized: true}, true); state != options.Normal {
		t.Fatal("admin restart did not show window")
	}
	if desktopInstanceID(root) == desktopInstanceID(filepath.Join(root, "other")) {
		t.Fatal("different data roots share lock")
	}
}
func TestDesktopPreferencesLegacyDefaultsAndSavedValues(t *testing.T) {
	root := t.TempDir()
	if prefs := readDesktopPreferences(root); prefs.CloseBehavior != "quit" || prefs.StartMinimized || prefs.AutoStart {
		t.Fatal(prefs)
	}
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state", "panel.json")
	if err := os.WriteFile(path, []byte(`{"settings":{"system":{"autoStart":true,"startMinimized":true,"closeBehavior":"tray"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if prefs := readDesktopPreferences(root); prefs.CloseBehavior != "tray" || !prefs.StartMinimized || !prefs.AutoStart {
		t.Fatal(prefs)
	}
}
