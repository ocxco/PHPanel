//go:build !windows

package panel

import "fmt"

type unavailablePHPRuntime struct{}

func currentRuntimeUserSID() (string, error) { return "", nil }
func validateRuntimeUserSID(string) error    { return nil }

func (unavailablePHPRuntime) Protect(Instance, bool) error { return nil }

func newPHPRuntimeBackend(string) phpRuntimeBackend { return unavailablePHPRuntime{} }
func (unavailablePHPRuntime) Query(Instance) (phpRuntimeStatus, error) {
	return phpRuntimeStatus{}, nil
}
func (unavailablePHPRuntime) Start(Instance, string, string) (phpRuntimeStatus, error) {
	return phpRuntimeStatus{}, fmt.Errorf("后台 PHP 托管仅支持 Windows")
}
func (unavailablePHPRuntime) Stop(Instance, string) error { return nil }
func (unavailablePHPRuntime) Diagnostic(Instance) (PHPProbe, error) {
	return PHPProbe{}, fmt.Errorf("未运行后台 PHP")
}
func servePHPRuntime(*Manager, Instance, *managedProcess) (runtimeServer, error) { return nil, nil }
func RunLinkedPHPHost(string, string, string, string) error {
	return fmt.Errorf("后台 PHP 托管仅支持 Windows")
}
