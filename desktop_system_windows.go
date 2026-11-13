//go:build windows

package main

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const runRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Run`

type windowsStartupRegistry struct{}

func newStartupRegistry() startupRegistry { return windowsStartupRegistry{} }
func startupCommand(exe, root string) string {
	args := []string{exe, startupArgument, dataRootArgument + root}
	for n := range args {
		args[n] = windows.EscapeArg(args[n])
	}
	return strings.Join(args, " ")
}
func (windowsStartupRegistry) Read() (autorunEntry, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runRegistryPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return autorunEntry{}, nil
	}
	if err != nil {
		return autorunEntry{}, err
	}
	defer key.Close()
	value, kind, err := key.GetStringValue("PHPanel")
	if errors.Is(err, registry.ErrNotExist) {
		return autorunEntry{}, nil
	}
	return autorunEntry{Exists: err == nil, Value: value, Kind: kind}, err
}
func (windowsStartupRegistry) Write(entry autorunEntry) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !entry.Exists {
		err := key.DeleteValue("PHPanel")
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	if entry.Kind == registry.EXPAND_SZ {
		return key.SetExpandStringValue("PHPanel", entry.Value)
	}
	return key.SetStringValue("PHPanel", entry.Value)
}
