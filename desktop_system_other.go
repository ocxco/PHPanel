//go:build !windows

package main

import "fmt"

type unavailableStartupRegistry struct{}

func newStartupRegistry() startupRegistry                      { return unavailableStartupRegistry{} }
func (unavailableStartupRegistry) Read() (autorunEntry, error) { return autorunEntry{}, nil }
func (unavailableStartupRegistry) Write(autorunEntry) error {
	return fmt.Errorf("开机启动仅支持 Windows")
}
func startupCommand(exe, root string) string {
	return exe + " " + startupArgument + " " + dataRootArgument + root
}
func newTrayIcon(func(), func()) (trayIcon, error) {
	return nil, fmt.Errorf("系统托盘仅支持 Windows")
}
