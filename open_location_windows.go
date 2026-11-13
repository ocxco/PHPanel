//go:build windows

package main

import (
	"os/exec"
	"phpanel/internal/panel"
)

func openLocation(location panel.Location) error {
	args := []string{location.Path}
	if location.IsFile {
		args = []string{"/select,", location.Path}
	}
	command := exec.Command("explorer.exe", args...)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
