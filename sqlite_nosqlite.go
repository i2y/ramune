//go:build nosqlite

package ramune

// sqliteManager is a no-op stub when built with -tags nosqlite.
type sqliteManager struct{}

func (m *sqliteManager) closeAll() {}

func (r *Runtime) installSQLite() error {
	return nil
}
