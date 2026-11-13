package main

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func restartFixture(t *testing.T) elevationRequest {
	t.Helper()
	enabled := false
	return elevationRequest{Version: 1, ParentPID: 4321, Nonce: strings.Repeat("ab", 32), DataRoot: filepath.Join(t.TempDir(), "space 中文"), ReturnView: "instances", InstanceID: "nginx-instance", ServiceAutoStart: &enabled, SoftwareRendering: "0", WebviewProfile: filepath.Join(t.TempDir(), "web view")}
}
func TestElevationRestartRequestRoundTripAndValidation(t *testing.T) {
	r := restartFixture(t)
	arg, err := encodeElevationRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := parseElevationRequest([]string{"unrelated-argument", arg})
	if err != nil {
		t.Fatal(err)
	}
	if actual == nil || actual.DataRoot != r.DataRoot || actual.InstanceID != r.InstanceID || actual.ParentPID != r.ParentPID || actual.WebviewProfile != r.WebviewProfile || actual.SoftwareRendering != "0" || actual.ServiceAutoStart == nil || *actual.ServiceAutoStart {
		t.Fatal(actual)
	}
	if _, err := parseElevationRequest([]string{arg, arg}); err == nil {
		t.Fatal("duplicate handoff accepted")
	}
	if _, err := parseElevationRequest([]string{elevationArgument + "invalid-base64"}); err == nil {
		t.Fatal("invalid handoff accepted")
	}
	r.ReturnView = "invalid"
	if _, err := encodeElevationRequest(r); err == nil {
		t.Fatal("invalid view accepted")
	}
	r = restartFixture(t)
	r.Nonce = "tiny"
	if _, err := encodeElevationRequest(r); err == nil {
		t.Fatal("short nonce accepted")
	}
	r = restartFixture(t)
	r.DataRoot = "relative"
	if _, err := encodeElevationRequest(r); err == nil {
		t.Fatal("relative root accepted")
	}
	r = restartFixture(t)
	r.InstanceID = "line\nbreak"
	if _, err := encodeElevationRequest(r); err == nil {
		t.Fatal("invalid characters accepted")
	}
	if value, err := parseElevationRequest(nil); value != nil || err != nil {
		t.Fatal("ordinary startup affected")
	}
	if _, err := parseElevationRequest([]string{elevationArgument + base64.RawURLEncoding.EncodeToString([]byte(`{"version":99}`))}); err == nil {
		t.Fatal("invalid schema accepted")
	}
}
func TestElevationCancellationAndFailurePreserveOldApp(t *testing.T) {
	for _, failure := range []error{errElevationCancelled, errors.New("launcher failed"), errors.New("child exited before ready")} {
		quit := false
		controller := elevationController{isElevated: func() bool { return false }, launch: func(string, elevationRequest) error { return failure }, quit: func() { quit = true }}
		result, err := controller.restart("PHPanel.exe", restartFixture(t))
		if quit {
			t.Fatal("failed or cancelled launch quit original app")
		}
		if errors.Is(failure, errElevationCancelled) {
			if err != nil || !result.Cancelled {
				t.Fatal(result, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "当前面板已保留") {
			t.Fatal(err)
		}
	}
}
func TestElevationRestartsOnlyAfterReadyAndPreventsDuplicates(t *testing.T) {
	ready, quit := false, false
	controller := elevationController{isElevated: func() bool { return false }, launch: func(_ string, r elevationRequest) error {
		if r.InstanceID != "nginx-instance" {
			t.Fatal(r)
		}
		ready = true
		return nil
	}, quit: func() {
		if !ready {
			t.Fatal("quit before ready")
		}
		quit = true
	}}
	if result, err := controller.restart("PHPanel.exe", restartFixture(t)); err != nil || result.Cancelled || !quit {
		t.Fatal(result, err)
	}
	controller.mu.Lock()
	_, err := controller.restart("PHPanel.exe", restartFixture(t))
	controller.mu.Unlock()
	if err == nil {
		t.Fatal("parallel restarts accepted")
	}
	controller.isElevated = func() bool { return true }
	ready, quit = false, false
	if _, err := controller.restart("PHPanel.exe", restartFixture(t)); err == nil || ready || quit {
		t.Fatal("already elevated app restarted", err)
	}
}

func TestElevationDataRootProbeDoesNotChangeStateOrLeaveFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "panel.json")
	body := []byte(`{"root":"unchanged"}`)
	if err := os.WriteFile(state, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyElevationDataRoot(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(state)
	if string(after) != string(body) {
		t.Fatal("probe changed metadata")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 || files[0].Name() != "panel.json" {
		t.Fatal("probe left artifacts", files)
	}
	if err := verifyElevationDataRoot(t.TempDir()); err == nil {
		t.Fatal("missing data root accepted")
	}
}
