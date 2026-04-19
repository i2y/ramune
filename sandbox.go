package ramune

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// SandboxConfig configures Docker sandbox execution.
type SandboxConfig struct {
	// Image is the Docker image to use. Default: "ubuntu:24.04".
	Image string
	// Mounts are additional bind mounts in "host:container[:ro]" format.
	Mounts []string
	// Env is additional environment variables for the container.
	Env map[string]string
	// Timeout is the maximum execution time. Default: 60s.
	Timeout time.Duration
	// SocketPath overrides the Docker socket path.
	SocketPath string
	// Network is the Docker network to connect the container to.
	// Use this to give the sandbox access to other services on the same network.
	Network string
	// ExtraHosts adds custom host-to-IP mappings (like --add-host).
	// Format: "hostname:ip" (e.g. "api-server:192.168.1.100").
	ExtraHosts []string
	// MemoryMB sets the container memory limit in megabytes. 0 = unlimited.
	MemoryMB int
	// CPUs sets the CPU limit (e.g. 1.5 = 1.5 cores). 0 = unlimited.
	CPUs float64
	// NoNetwork disables all network access from the container.
	NoNetwork bool
}

// SandboxResult holds the result of a sandboxed execution.
type SandboxResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// SandboxRuntime holds Go function registrations for sandbox execution.
// When the binary is re-invoked inside Docker, the same functions are
// available because they are compiled into the binary.
type SandboxRuntime struct {
	opts  []Option
	funcs map[string]GoFunc
}

// NewSandboxRuntime creates a new SandboxRuntime with the given options.
func NewSandboxRuntime(opts ...Option) *SandboxRuntime {
	return &SandboxRuntime{
		opts:  opts,
		funcs: make(map[string]GoFunc),
	}
}

// RegisterFunc registers a Go function that will be available to JS code
// in the sandbox. The function is compiled into the binary, so it works
// both on the host and inside the Docker container.
func (s *SandboxRuntime) RegisterFunc(name string, fn GoFunc) {
	s.funcs[name] = fn
}

// SandboxRun executes a ramune script inside a Docker container.
func (s *SandboxRuntime) SandboxRun(scriptPath string, cfg SandboxConfig) (*SandboxResult, error) {
	if isSandboxWorker() {
		return s.runAsWorker([]string{"run", scriptPath})
	}
	return sandboxExec([]string{"run", scriptPath}, cfg)
}

// SandboxEval evaluates JS code inside a Docker container.
func (s *SandboxRuntime) SandboxEval(code string, cfg SandboxConfig) (*SandboxResult, error) {
	if isSandboxWorker() {
		return s.runAsWorker([]string{"eval", code})
	}
	return sandboxExec([]string{"eval", code}, cfg)
}

// runAsWorker executes as the sandbox worker (inside Docker).
// Creates a real Runtime, registers all functions, runs the script.
func (s *SandboxRuntime) runAsWorker(args []string) (*SandboxResult, error) {
	rt, err := New(s.opts...)
	if err != nil {
		return &SandboxResult{ExitCode: 1, Stderr: err.Error()}, nil
	}
	defer rt.Close()

	for name, fn := range s.funcs {
		if err := rt.RegisterFunc(name, fn); err != nil {
			return &SandboxResult{ExitCode: 1, Stderr: err.Error()}, nil
		}
	}

	if args[0] == "eval" {
		v, err := rt.Eval(args[1])
		if err != nil {
			return &SandboxResult{ExitCode: 1, Stderr: err.Error()}, nil
		}
		s := v.String()
		v.Close()
		return &SandboxResult{ExitCode: 0, Stdout: s}, nil
	}

	// Run file.
	code, err := os.ReadFile(args[1])
	if err != nil {
		return &SandboxResult{ExitCode: 1, Stderr: err.Error()}, nil
	}
	if err := rt.Exec(string(code)); err != nil {
		return &SandboxResult{ExitCode: 1, Stderr: err.Error()}, nil
	}
	rt.RunEventLoop()
	return &SandboxResult{ExitCode: 0}, nil
}

const sandboxEnvKey = "RAMUNE_SANDBOX_WORKER"

func isSandboxWorker() bool {
	return os.Getenv(sandboxEnvKey) == "1"
}

// HandleSandboxWorker checks if the current process is a sandbox worker
// and returns true if so. The caller should pass the SandboxRuntime
// that was set up with RegisterFunc calls, then exit.
//
// Usage:
//
//	rt := ramune.NewSandboxRuntime(ramune.NodeCompat())
//	rt.RegisterFunc("add", func(a, b float64) float64 { return a + b })
//	if ramune.HandleSandboxWorker(rt) {
//	    return
//	}
func HandleSandboxWorker(s *SandboxRuntime) bool {
	if !isSandboxWorker() {
		return false
	}
	args := os.Args[1:]
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "sandbox worker: insufficient arguments\n")
		os.Exit(1)
	}
	result, _ := s.runAsWorker(args)
	if result.Stdout != "" {
		os.Stdout.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		os.Stderr.WriteString(result.Stderr)
	}
	os.Exit(result.ExitCode)
	return true // unreachable
}

// Package-level functions for CLI usage (no Go function bindings).

// SandboxRun executes a ramune script inside a Docker container.
func SandboxRun(scriptPath string, cfg SandboxConfig) (*SandboxResult, error) {
	return sandboxExec([]string{"run", scriptPath}, cfg)
}

// SandboxEval evaluates a JS expression inside a Docker container.
func SandboxEval(code string, cfg SandboxConfig) (*SandboxResult, error) {
	return sandboxExec([]string{"eval", code}, cfg)
}

func sandboxExec(args []string, cfg SandboxConfig) (*SandboxResult, error) {
	if cfg.Image == "" {
		cfg.Image = "ubuntu:24.04"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}

	dc := newDockerClient(cfg.SocketPath)
	if err := dc.ping(); err != nil {
		return nil, fmt.Errorf("sandbox: docker not reachable: %w", err)
	}

	if _, err := dc.imageInspect(cfg.Image); err != nil {
		if err := dc.imagePull(cfg.Image); err != nil {
			return nil, fmt.Errorf("sandbox: cannot pull image %s: %w", cfg.Image, err)
		}
	}

	exePath, tmpBin, err := prepareSandboxBinary()
	if err != nil {
		return nil, err
	}
	if tmpBin != "" {
		defer os.Remove(tmpBin)
	}

	binds := []string{exePath + ":/usr/local/bin/ramune:ro"}

	containerArgs := []string{"/usr/local/bin/ramune"}
	if args[0] == "run" {
		scriptAbs, err := absPath(args[1])
		if err != nil {
			return nil, fmt.Errorf("sandbox: invalid script path: %w", err)
		}
		containerScript := "/work/" + baseName(scriptAbs)
		binds = append(binds, scriptAbs+":"+containerScript+":ro")
		containerArgs = append(containerArgs, "run", containerScript)
	} else {
		containerArgs = append(containerArgs, args...)
	}

	for _, m := range cfg.Mounts {
		binds = append(binds, m)
	}

	env := []string{sandboxEnvKey + "=1"}
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}

	hostConfig := map[string]any{
		"Binds":      binds,
		"AutoRemove": false,
	}
	if cfg.Network != "" {
		hostConfig["NetworkMode"] = cfg.Network
	}
	if cfg.NoNetwork {
		hostConfig["NetworkMode"] = "none"
	}
	if len(cfg.ExtraHosts) > 0 {
		hostConfig["ExtraHosts"] = cfg.ExtraHosts
	}
	if cfg.MemoryMB > 0 {
		hostConfig["Memory"] = cfg.MemoryMB * 1024 * 1024
	}
	if cfg.CPUs > 0 {
		hostConfig["NanoCpus"] = int64(cfg.CPUs * 1e9)
	}

	containerOpts := map[string]any{
		"Image":      cfg.Image,
		"Cmd":        containerArgs,
		"Env":        env,
		"HostConfig": hostConfig,
	}

	containerID, err := dc.createContainer(containerOpts)
	if err != nil {
		return nil, fmt.Errorf("sandbox: create container: %w", err)
	}
	defer dc.removeContainer(containerID, true)

	if err := dc.startContainer(containerID); err != nil {
		return nil, fmt.Errorf("sandbox: start container: %w", err)
	}

	type waitResult struct {
		code int
		err  error
	}
	waitCh := make(chan waitResult, 1)
	go func() {
		code, err := dc.waitContainer(containerID)
		waitCh <- waitResult{code, err}
	}()

	var exitCode int
	select {
	case wr := <-waitCh:
		if wr.err != nil {
			return nil, fmt.Errorf("sandbox: wait: %w", wr.err)
		}
		exitCode = wr.code
	case <-time.After(cfg.Timeout):
		dc.stopContainer(containerID, 3)
		return nil, fmt.Errorf("sandbox: timeout after %v", cfg.Timeout)
	}

	stdout, stderr := collectLogs(dc, containerID)

	return &SandboxResult{
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
	}, nil
}

func collectLogs(dc *dockerClient, containerID string) (string, string) {
	// Collect stdout.
	stdoutReader, err := dc.containerLogStream(containerID, true, false)
	if err != nil {
		return "", ""
	}
	stdoutData, _ := io.ReadAll(stdoutReader)
	stdoutReader.Close()

	// Collect stderr.
	stderrReader, err := dc.containerLogStream(containerID, false, true)
	if err != nil {
		return string(stdoutData), ""
	}
	stderrData, _ := io.ReadAll(stderrReader)
	stderrReader.Close()

	return string(stdoutData), string(stderrData)
}

func (c *dockerClient) containerLogStream(id string, stdout, stderr bool) (io.ReadCloser, error) {
	path := fmt.Sprintf("/containers/%s/logs?follow=false&stdout=%v&stderr=%v", id, stdout, stderr)
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("docker logs %s: %s (status %d)", id, string(data), resp.StatusCode)
	}
	return resp.Body, nil
}

func evalSymlinks(p string) (string, error) {
	for i := 0; i < 10; i++ {
		target, err := os.Readlink(p)
		if err != nil {
			return p, nil
		}
		if !isAbs(target) {
			p = dirName(p) + "/" + target
		} else {
			p = target
		}
	}
	return p, nil
}

func absPath(p string) (string, error) {
	if isAbs(p) {
		return p, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd + "/" + p, nil
}

func isAbs(p string) bool { return len(p) > 0 && p[0] == '/' }
func baseName(p string) string {
	i := strings.LastIndex(p, "/")
	if i >= 0 {
		return p[i+1:]
	}
	return p
}
func dirName(p string) string {
	i := strings.LastIndex(p, "/")
	if i >= 0 {
		return p[:i]
	}
	return "."
}

// SandboxAvailable checks whether Docker is reachable.
func SandboxAvailable() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	dc := newDockerClient("")
	return dc.ping() == nil
}

func prepareSandboxBinary() (binPath string, tmpFile string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("sandbox: cannot find executable: %w", err)
	}
	exe, err = evalSymlinks(exe)
	if err != nil {
		return "", "", fmt.Errorf("sandbox: cannot resolve executable: %w", err)
	}

	if runtime.GOOS == "linux" {
		return exe, "", nil
	}

	goArch := runtime.GOARCH
	if goArch == "" {
		goArch = "amd64"
	}

	cacheDir, _ := os.UserCacheDir()
	if cacheDir == "" {
		cacheDir = os.TempDir()
	}
	cachePath := cacheDir + "/ramune-sandbox-linux-" + goArch
	if info, err := os.Stat(cachePath); err == nil && info.Size() > 0 {
		exeInfo, _ := os.Stat(exe)
		if exeInfo != nil && !exeInfo.ModTime().After(info.ModTime()) {
			return cachePath, "", nil
		}
	}

	// Find the Go module root for the current executable.
	modRoot := findModRoot(exe)
	if modRoot == "" {
		wd, _ := os.Getwd()
		modRoot = findModRoot(wd + "/.")
	}
	if modRoot == "" {
		return "", "", fmt.Errorf("sandbox: cannot find go.mod for cross-compilation. " +
			"Pre-build with: GOOS=linux go build -tags qjswasm -o ramune-linux")
	}

	// Detect the main package to build. Use `go version -m` on the binary
	// to extract the module path, then build from source.
	buildTarget := detectBuildTarget(exe, modRoot)
	fmt.Fprintf(os.Stderr, "sandbox: cross-compiling %s for linux/%s (first run only)...\n", buildTarget, goArch)
	cmd := exec.Command("go", "build", "-tags", "qjswasm", "-o", cachePath, buildTarget)
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goArch, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(cachePath)
		return "", "", fmt.Errorf("sandbox: cross-compile failed: %s\n%s", err, string(out))
	}
	os.Chmod(cachePath, 0o755)
	fmt.Fprintf(os.Stderr, "sandbox: cross-compile done, cached at %s\n", cachePath)

	return cachePath, "", nil
}

func findModRoot(startFrom string) string {
	dir := dirName(startFrom)
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		parent := dirName(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func detectBuildTarget(exe, modRoot string) string {
	// Try `go version -m` to get the path from the binary's build info.
	out, err := exec.Command("go", "version", "-m", exe).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "path") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					pkg := parts[1]
					// Convert module path to relative: github.com/i2y/ramune/cmd/ramune → ./cmd/ramune
					modPath := ""
					if modData, err := os.ReadFile(modRoot + "/go.mod"); err == nil {
						for _, ml := range strings.Split(string(modData), "\n") {
							if strings.HasPrefix(ml, "module ") {
								modPath = strings.TrimSpace(strings.TrimPrefix(ml, "module "))
								break
							}
						}
					}
					if modPath != "" && strings.HasPrefix(pkg, modPath) {
						rel := strings.TrimPrefix(pkg, modPath)
						if rel == "" {
							return "."
						}
						return "." + rel
					}
					return pkg
				}
			}
		}
	}
	// Fallback: if exe is in a cmd/ subdirectory, use that.
	if idx := strings.Index(exe, "/cmd/"); idx >= 0 {
		return "." + exe[idx:]
	}
	return "."
}

// marshalResult encodes a SandboxResult as JSON for worker→host communication.
func marshalResult(r *SandboxResult) string {
	data, _ := json.Marshal(r)
	return string(data)
}
