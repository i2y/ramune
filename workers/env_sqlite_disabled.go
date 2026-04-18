//go:build nosqlite

package workers

import (
	"fmt"

	"github.com/i2y/ramune"
)

// installSQLiteBinds returns an error when the workers package has been
// built with the nosqlite tag. The caller (Register) surfaces this to
// the user so opt-in SQLite usage fails loudly rather than silently.
func installSQLiteBinds(_ *ramune.Runtime, cfg *Config) error {
	return fmt.Errorf("workers: WithSQLite(%q) requires building without the 'nosqlite' build tag", cfg.SQLitePath)
}
