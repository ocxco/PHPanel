package panel

import "fmt"

func NormalizeSystemSettings(settings SystemSettings) (SystemSettings, error) {
	if settings.CloseBehavior == "" {
		settings.CloseBehavior = "quit"
	}
	if settings.CloseBehavior != "quit" && settings.CloseBehavior != "tray" {
		return settings, fmt.Errorf("未知关闭按钮行为")
	}
	return settings, nil
}
func (m *Manager) SaveSystemSettings(settings SystemSettings) error {
	finish, err := m.trackServiceAction()
	if err != nil {
		return err
	}
	defer finish()
	settings, err = NormalizeSystemSettings(settings)
	if err != nil {
		return err
	}
	return m.change(func(s *State) error { s.Settings.System = settings; return nil })
}
