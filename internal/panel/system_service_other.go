//go:build !windows

package panel

import "fmt"

type runtimeLease struct{}

func acquireRuntimeLease(string, Instance) (*runtimeLease, error) { return &runtimeLease{}, nil }
func (*runtimeLease) release()                                    {}
func newSystemServiceBackend(string) systemServiceBackend         { return nil }
func RunSystemServiceHost(string, string, string) error {
	return fmt.Errorf("系统服务仅适用于 Windows")
}
