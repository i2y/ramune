//go:build notui

package ramune

// installTUI is a no-op when built with -tags notui. The Charm TUI
// stack (Bubbletea / Lipgloss / glamour / wish) and its ~25 transitive
// modules — including an SSH server — are dropped from the binary;
// scripts that reference globalThis.Ramune.tui hit an undefined value.
func (r *Runtime) installTUI() error {
	return nil
}
