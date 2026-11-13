package panel

import "fmt"

func validTheme(mode string) bool { return mode == "light" || mode == "dark" || mode == "system" }
func (m *Manager) SaveTheme(mode string) error {
	if !validTheme(mode) {
		return fmt.Errorf("未知主题模式")
	}
	return m.change(func(s *State) error { s.Settings.ThemeMode = mode; return nil })
}
