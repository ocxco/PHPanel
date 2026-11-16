package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	if d.beforeClose(panel.SystemSettings{CloseBehavior: "quit", CloseBehaviorConfirmed: true}) || hides != 0 {
		t.Fatal("quit behavior hid window")
	}
	if !d.beforeClose(panel.SystemSettings{CloseBehavior: "tray", CloseBehaviorConfirmed: true}) || hides != 1 || quits != 0 {
		t.Fatal("tray behavior quit app")
	}
	d.exit()
	if quits != 1 || d.beforeClose(panel.SystemSettings{CloseBehavior: "tray", CloseBehaviorConfirmed: true}) {
		t.Fatal("explicit tray exit was intercepted")
	}
	d.forceExit.Store(false)
	d.tray = &fakeTray{ready: false}
	if d.beforeClose(panel.SystemSettings{CloseBehavior: "tray", CloseBehaviorConfirmed: true}) {
		t.Fatal("unavailable tray stranded hidden window")
	}
}
func TestSystemPreferenceSaveRollsBackStartupAndRetainsForeignEntry(t *testing.T) {
	old := autorunEntry{Exists: true, Value: "previous app", Kind: 2}
	r := &fakeStartupRegistry{entry: old}
	d := desktopController{registry: r, tray: &fakeTray{ready: true}}
	settings := panel.SystemSettings{AutoStart: true, StartMinimized: true, CloseBehavior: "tray", CloseBehaviorConfirmed: true}
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
	if !desktopStartHidden(panel.SystemSettings{StartMinimized: true}, false) {
		t.Fatal("silent startup did not hide window")
	}
	if desktopStartHidden(panel.SystemSettings{StartMinimized: true}, true) {
		t.Fatal("admin restart did not show window")
	}
	if desktopStartHidden(panel.SystemSettings{}, false) {
		t.Fatal("normal startup hid window")
	}
	if desktopInstanceID(root) == desktopInstanceID(filepath.Join(root, "other")) {
		t.Fatal("different data roots share lock")
	}
}
func TestDesktopPreferencesLegacyDefaultsAndSavedValues(t *testing.T) {
	root := t.TempDir()
	if prefs := readDesktopPreferences(root); prefs.CloseBehavior != "quit" || prefs.StartMinimized || prefs.AutoStart || prefs.CloseBehaviorConfirmed {
		t.Fatal(prefs)
	}
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state", "panel.json")
	if err := os.WriteFile(path, []byte(`{"settings":{"system":{"autoStart":true,"startMinimized":true,"closeBehavior":"tray"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if prefs := readDesktopPreferences(root); prefs.CloseBehavior != "tray" || !prefs.StartMinimized || !prefs.AutoStart || prefs.CloseBehaviorConfirmed {
		t.Fatal(prefs)
	}
	if err := os.WriteFile(path, []byte(`{"settings":{"system":{"closeBehavior":"tray","closeBehaviorConfirmed":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !readDesktopPreferences(root).CloseBehaviorConfirmed {
		t.Fatal("saved close choice was lost")
	}
}

func TestDesktopFirstCloseChoiceCancellationAndPersistence(t *testing.T) {
	for _, behavior := range []string{"quit", "tray"} {
		t.Run(behavior, func(t *testing.T) {
			prompts, hides, quits, saves := 0, 0, 0, 0
			d := desktopController{tray: &fakeTray{ready: true}, promptClose: func() { prompts++ }, hide: func() { hides++ }, quit: func() { quits++ }}
			settings := panel.SystemSettings{AutoStart: true, StartMinimized: true, CloseBehavior: behavior}
			if !d.beforeClose(settings) || !d.pendingCloseChoice() || prompts != 1 || hides != 0 || quits != 0 {
				t.Fatal("first close did not wait for choice")
			}
			d.cancelCloseChoice()
			if d.pendingCloseChoice() || hides != 0 || quits != 0 {
				t.Fatal("cancel applied close behavior")
			}
			persist := func(actual panel.SystemSettings) error {
				saves++
				if !actual.CloseBehaviorConfirmed || !actual.AutoStart || !actual.StartMinimized || actual.CloseBehavior != behavior {
					t.Fatal("choice replaced other settings", actual)
				}
				if hides != 0 || quits != 0 {
					t.Fatal("window closed before saving choice")
				}
				settings = actual
				return nil
			}
			if err := d.selectCloseBehavior(settings, behavior, persist); err == nil || saves != 0 {
				t.Fatal("cancelled choice was accepted")
			}
			d.beforeClose(settings)
			if err := d.selectCloseBehavior(settings, behavior, persist); err != nil || d.pendingCloseChoice() || saves != 1 {
				t.Fatal("choice was not saved", err)
			}
			if behavior == "quit" && (quits != 1 || hides != 0) || behavior == "tray" && (hides != 1 || quits != 0) {
				t.Fatal("incorrect close action", hides, quits)
			}
			d.beforeClose(settings)
			if prompts != 2 {
				t.Fatal("confirmed behavior prompted again")
			}
		})
	}
}

func TestDesktopCloseChoiceFailureKeepsWindowAndExplicitExitBypassesPrompt(t *testing.T) {
	prompts, hides, quits, saves := 0, 0, 0, 0
	d := desktopController{tray: &fakeTray{ready: false}, promptClose: func() { prompts++ }, hide: func() { hides++ }, quit: func() { quits++ }}
	settings := panel.SystemSettings{CloseBehavior: "tray"}
	d.beforeClose(settings)
	persist := func(panel.SystemSettings) error { saves++; return errors.New("disk full") }
	for _, behavior := range []string{"invalid", "tray"} {
		if err := d.selectCloseBehavior(settings, behavior, persist); err == nil || saves != 0 {
			t.Fatal("invalid or unavailable close behavior saved", behavior)
		}
	}
	if err := d.selectCloseBehavior(settings, "quit", persist); err == nil || !d.pendingCloseChoice() || hides != 0 || quits != 0 {
		t.Fatal("failed persistence closed window")
	}
	d.exit()
	if quits != 1 || d.beforeClose(settings) || prompts != 1 {
		t.Fatal("explicit exit was intercepted by first-close prompt")
	}
}

func TestDesktopSilentStartupRestoresWindowOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		hidden, tray bool
		initErr      error
		shows        int
	}{
		{"silent", true, true, nil, 0},
		{"normal", false, false, nil, 0},
		{"tray-failed", true, false, nil, 1},
		{"init-failed", true, true, errors.New("initialization failed"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shows := 0
			ready := make(chan struct{})
			close(ready)
			a := App{startHidden: tc.hidden, initErr: tc.initErr, desktop: &desktopController{startupReady: ready, tray: &fakeTray{ready: tc.tray}, show: func() { shows++ }}}
			a.finishDesktopStartup(context.Background())
			if shows != tc.shows {
				t.Fatal("startup window visibility", shows)
			}
		})
	}
}
