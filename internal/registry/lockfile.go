package registry

import (
	"encoding/json"
	"os"
)

// Lockfile represents the ramune.lock file for reproducible installs.
type Lockfile struct {
	Version  int                      `json:"version"`
	Packages map[string]LockfileEntry `json:"packages"`
}

// LockfileEntry is a single locked package.
type LockfileEntry struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Integrity    string            `json:"integrity"`
	Tarball      string            `json:"tarball"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// ReadLockfile reads a ramune.lock file. Returns nil if the file does not exist.
func ReadLockfile(path string) (*Lockfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, err
	}
	return &lf, nil
}

// WriteLockfile writes a ramune.lock file.
func WriteLockfile(path string, lf *Lockfile) error {
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LockfileFromResolved creates a Lockfile from resolved packages.
func LockfileFromResolved(resolved map[string]*ResolvedPackage) *Lockfile {
	lf := &Lockfile{
		Version:  1,
		Packages: make(map[string]LockfileEntry, len(resolved)),
	}
	for _, pkg := range resolved {
		key := pkg.Name + "@" + pkg.Version
		lf.Packages[key] = LockfileEntry{
			Name:         pkg.Name,
			Version:      pkg.Version,
			Integrity:    pkg.Integrity,
			Tarball:      pkg.Tarball,
			Dependencies: pkg.Dependencies,
		}
	}
	return lf
}
