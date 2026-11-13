//go:build windows

package panel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestInstallRenameRetriesWindowsErrors(t *testing.T) {
	for _, code := range []error{windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION} {
		t.Run(code.Error(), func(t *testing.T) {
			dir := t.TempDir()
			source, target := filepath.Join(dir, "stage"), filepath.Join(dir, "published")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "keep.txt"), []byte("complete"), 0600); err != nil {
				t.Fatal(err)
			}
			attempts := 0
			err := retryInstallRename(context.Background(), source, target, time.Second, func(old, new string) error {
				attempts++
				if attempts < 3 {
					return &os.LinkError{Op: "rename", Old: old, New: new, Err: code}
				}
				return os.Rename(old, new)
			})
			if err != nil || attempts != 3 {
				t.Fatalf("attempts=%d err=%v", attempts, err)
			}
			body, err := os.ReadFile(filepath.Join(target, "keep.txt"))
			if err != nil || string(body) != "complete" {
				t.Fatal("staged files lost")
			}
		})
	}
}
func TestInstallRenameDoesNotRetryPermanentErrorsOrOverwrite(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "stage"), filepath.Join(dir, "published")
	permanent := &os.LinkError{Op: "rename", Old: source, New: target, Err: windows.ERROR_INVALID_NAME}
	attempts := 0
	err := retryInstallRename(context.Background(), source, target, time.Second, func(string, string) error { attempts++; return permanent })
	if !errors.Is(err, windows.ERROR_INVALID_NAME) || attempts != 1 {
		t.Fatalf("permanent error was retried: %v (%d)", err, attempts)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(target, "keep.txt"), []byte("existing"), 0600)
	err = retryInstallRename(context.Background(), source, target, time.Second, func(string, string) error { t.Fatal("rename called for an existing destination"); return nil })
	if err == nil {
		t.Fatal("existing destination accepted")
	}
	body, _ := os.ReadFile(filepath.Join(target, "keep.txt"))
	if string(body) != "existing" {
		t.Fatal("existing file changed")
	}
}
func TestInstallRenameTimeoutAndCancellation(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "stage"), filepath.Join(dir, "published")
	start := time.Now()
	err := retryInstallRename(context.Background(), source, target, 20*time.Millisecond, func(string, string) error { return windows.ERROR_ACCESS_DENIED })
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || time.Since(start) > time.Second {
		t.Fatalf("retry did not stop: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	first := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- retryInstallRename(ctx, source, target, 10*time.Second, func(string, string) error {
			if attempts.Add(1) == 1 {
				close(first)
			}
			return windows.ERROR_SHARING_VIOLATION
		})
	}()
	<-first
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop retry")
	}
}

func TestInstallRenameWaitsForActualWindowsFileHandle(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "stage"), filepath.Join(dir, "published")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(source, "php-cgi.exe")
	if err := os.WriteFile(file, fakePE(), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(file)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			windows.CloseHandle(handle)
		}
	}()
	if err := os.Rename(source, target); !retryableInstallRename(err) {
		t.Fatalf("expected a real Windows sharing error: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- renameInstallDirectory(context.Background(), source, target) }()
	select {
	case err := <-done:
		t.Fatalf("publish finished while file was locked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	closed = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publish did not resume after handle release")
	}
	if _, err := findExecutable(target, "php"); err != nil {
		t.Fatal(err)
	}
}
