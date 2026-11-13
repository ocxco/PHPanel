package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"phpanel/internal/panel"
	"strings"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	command := strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
	args := os.Args[1:]
	root := filepath.Dir(filepath.Dir(exe))
	if command == "phpanel-cli" && len(args) >= 2 && args[0] == "--phpanel-command" {
		command = args[1]
		args = args[2:]
	} else if command == "phpanel-cli" {
		fmt.Fprintln(os.Stderr, "该程序由 PHPanel 生成 PHP/Composer 命令入口使用。")
		os.Exit(1)
	}
	invocation, err := panel.ResolveCLIInvocation(root, command, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "PHPanel:", err)
		os.Exit(1)
	}
	cmd := exec.Command(invocation.Executable, invocation.Args...)
	cmd.Env = invocation.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
