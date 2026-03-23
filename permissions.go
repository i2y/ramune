package ramune

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PermissionState represents the state of a permission.
type PermissionState int

const (
	PermGranted PermissionState = iota
	PermDenied
)

// Permissions controls access to system resources.
// Default is all granted (Bun-compatible). Use WithSandbox() to deny by default.
type Permissions struct {
	Read  PermissionState
	Write PermissionState
	Net   PermissionState
	Env   PermissionState
	Run   PermissionState

	ReadPaths  []string // allowed read paths (empty = all if granted)
	WritePaths []string // allowed write paths
	NetHosts   []string // allowed network hosts
	EnvVars    []string // allowed env var names
	RunCmds    []string // allowed commands
}

// AllPermissions returns permissions with everything granted.
func AllPermissions() *Permissions {
	return &Permissions{
		Read: PermGranted, Write: PermGranted,
		Net: PermGranted, Env: PermGranted, Run: PermGranted,
	}
}

// SandboxPermissions returns permissions with everything denied.
func SandboxPermissions() *Permissions {
	return &Permissions{
		Read: PermDenied, Write: PermDenied,
		Net: PermDenied, Env: PermDenied, Run: PermDenied,
	}
}

// CheckRead checks if reading the given path is allowed.
func (p *Permissions) CheckRead(path string) error {
	if p == nil {
		return nil
	}
	if p.Read == PermGranted && len(p.ReadPaths) == 0 {
		return nil
	}
	if p.Read == PermDenied && len(p.ReadPaths) == 0 {
		return fmt.Errorf("PermissionDenied: read access to '%s'. Use --allow-read to grant", path)
	}
	abs, _ := filepath.Abs(path)
	for _, allowed := range p.ReadPaths {
		allowedAbs, _ := filepath.Abs(allowed)
		if abs == allowedAbs || strings.HasPrefix(abs, allowedAbs+"/") {
			return nil
		}
	}
	return fmt.Errorf("PermissionDenied: read access to '%s'", path)
}

// CheckWrite checks if writing to the given path is allowed.
func (p *Permissions) CheckWrite(path string) error {
	if p == nil {
		return nil
	}
	if p.Write == PermGranted && len(p.WritePaths) == 0 {
		return nil
	}
	if p.Write == PermDenied && len(p.WritePaths) == 0 {
		return fmt.Errorf("PermissionDenied: write access to '%s'. Use --allow-write to grant", path)
	}
	abs, _ := filepath.Abs(path)
	for _, allowed := range p.WritePaths {
		allowedAbs, _ := filepath.Abs(allowed)
		if abs == allowedAbs || strings.HasPrefix(abs, allowedAbs+"/") {
			return nil
		}
	}
	return fmt.Errorf("PermissionDenied: write access to '%s'", path)
}

// CheckNet checks if network access to the given host is allowed.
func (p *Permissions) CheckNet(host string) error {
	if p == nil || p.Net == PermGranted {
		if len(p.NetHosts) == 0 {
			return nil
		}
	}
	if p.Net == PermDenied && len(p.NetHosts) == 0 {
		return fmt.Errorf("PermissionDenied: network access to '%s'. Use --allow-net to grant", host)
	}
	for _, allowed := range p.NetHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return nil
		}
	}
	if p.Net == PermDenied {
		return fmt.Errorf("PermissionDenied: network access to '%s'", host)
	}
	return nil
}

// CheckEnv checks if accessing the given env var is allowed.
func (p *Permissions) CheckEnv(name string) error {
	if p == nil || p.Env == PermGranted {
		return nil
	}
	if len(p.EnvVars) == 0 {
		return fmt.Errorf("PermissionDenied: env access to '%s'. Use --allow-env to grant", name)
	}
	for _, allowed := range p.EnvVars {
		if name == allowed {
			return nil
		}
	}
	return fmt.Errorf("PermissionDenied: env access to '%s'", name)
}

// CheckRun checks if running the given command is allowed.
func (p *Permissions) CheckRun(cmd string) error {
	if p == nil || p.Run == PermGranted {
		return nil
	}
	if len(p.RunCmds) == 0 {
		return fmt.Errorf("PermissionDenied: run '%s'. Use --allow-run to grant", cmd)
	}
	for _, allowed := range p.RunCmds {
		if cmd == allowed {
			return nil
		}
	}
	return fmt.Errorf("PermissionDenied: run '%s'", cmd)
}

// WithPermissions sets the permission policy for the Runtime.
func WithPermissions(p *Permissions) Option {
	return func(c *config) { c.permissions = p }
}
