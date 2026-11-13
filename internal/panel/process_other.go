//go:build !windows

package panel

import "os/exec"

type processGuard struct{ cmd *exec.Cmd }

func nativePath(path string) string { return path }

func hideWindow(cmd *exec.Cmd)                          {}
func guardProcess(cmd *exec.Cmd) (*processGuard, error) { return &processGuard{cmd: cmd}, nil }
func (g *processGuard) kill() error                     { return g.cmd.Process.Kill() }
func (g *processGuard) close()                          {}
