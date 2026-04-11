package ramune

import (
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
}

// SandboxResult holds the result of a sandboxed execution.
type SandboxResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// SandboxRun executes a ramune script inside a Docker container.
// The current Go binary is mounted into the container and re-invoked
// with the given script, preserving all compiled-in Go functions.
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

	// Ensure image exists, pull if needed.
	if _, err := dc.imageInspect(cfg.Image); err != nil {
		if err := dc.imagePull(cfg.Image); err != nil {
			return nil, fmt.Errorf("sandbox: cannot pull image %s: %w", cfg.Image, err)
		}
	}

	// Determine the binary to mount into the container.
	// If host OS != linux, cross-compile a Linux binary.
	exePath, tmpBin, err := prepareSandboxBinary()
	if err != nil {
		return nil, err
	}
	if tmpBin != "" {
		defer os.Remove(tmpBin)
	}

	// Build container config.
	binds := []string{exePath + ":/usr/local/bin/ramune:ro"}

	// Mount script file if running a file (not eval).
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

	env := make([]string, 0, len(cfg.Env))
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}

	containerOpts := map[string]any{
		"Image": cfg.Image,
		"Cmd":   containerArgs,
		"Env":   env,
		"HostConfig": map[string]any{
			"Binds":      binds,
			"AutoRemove": false,
		},
	}

	containerID, err := dc.createContainer(containerOpts)
	if err != nil {
		return nil, fmt.Errorf("sandbox: create container: %w", err)
	}

	// Always clean up the container.
	defer dc.removeContainer(containerID, true)

	if err := dc.startContainer(containerID); err != nil {
		return nil, fmt.Errorf("sandbox: start container: %w", err)
	}

	// Wait for completion with timeout.
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

	// Collect logs (stdout + stderr multiplexed).
	stdout, stderr := collectLogs(dc, containerID)

	return &SandboxResult{
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
	}, nil
}

func collectLogs(dc *dockerClient, containerID string) (string, string) {
	reader, err := dc.containerLogs(containerID, false)
	if err != nil {
		return "", ""
	}
	defer reader.Close()

	// Docker multiplexed stream: 8-byte header per frame.
	// header[0]: stream type (1=stdout, 2=stderr)
	// header[4:8]: frame size (big-endian uint32)
	var stdout, stderr strings.Builder
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(reader, header); err != nil {
			break
		}
		size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
		if size <= 0 {
			continue
		}
		frame := make([]byte, size)
		if _, err := io.ReadFull(reader, frame); err != nil {
			break
		}
		switch header[0] {
		case 1:
			stdout.Write(frame)
		case 2:
			stderr.Write(frame)
		default:
			stdout.Write(frame)
		}
	}
	return stdout.String(), stderr.String()
}

// Helper functions to avoid importing path/filepath in this file
// (it's already used elsewhere, but keeping sandbox.go self-contained).
func evalSymlinks(p string) (string, error) {
	// Resolve symlinks by reading /proc/self/exe on Linux
	// or using os.Readlink chain on other platforms.
	for i := 0; i < 10; i++ {
		target, err := os.Readlink(p)
		if err != nil {
			return p, nil // not a symlink
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

func isAbs(p string) bool {
	return len(p) > 0 && p[0] == '/'
}

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

// prepareSandboxBinary returns the path to a Linux binary suitable for
// mounting into a Docker container. If the host is already Linux, it
// returns the current executable. Otherwise it cross-compiles a Linux
// binary to a temp file and returns its path (caller must clean up).
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

	// Check for cached cross-compiled binary.
	cacheDir, _ := os.UserCacheDir()
	if cacheDir == "" {
		cacheDir = os.TempDir()
	}
	cachePath := cacheDir + "/ramune-sandbox-linux-" + goArch
	if info, err := os.Stat(cachePath); err == nil && info.Size() > 0 {
		// Check if cached binary is newer than the source executable.
		exeInfo, _ := os.Stat(exe)
		if exeInfo != nil && !exeInfo.ModTime().After(info.ModTime()) {
			return cachePath, "", nil
		}
	}

	// Find the Go module root by looking for go.mod.
	modRoot := findModRoot(exe)
	if modRoot == "" {
		return "", "", fmt.Errorf("sandbox: cannot find go.mod for cross-compilation. " +
			"Pre-build a Linux binary with: GOOS=linux go build -tags quickjs -o ramune-linux ./cmd/ramune/")
	}

	fmt.Fprintf(os.Stderr, "sandbox: cross-compiling for linux/%s (first run only)...\n", goArch)
	cmd := exec.Command("go", "build", "-tags", "quickjs", "-o", cachePath, "./cmd/ramune/")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goArch, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(cachePath)
		return "", "", fmt.Errorf("sandbox: cross-compile failed: %s\n%s", err, string(out))
	}
	fmt.Fprintf(os.Stderr, "sandbox: cross-compile done, cached at %s\n", cachePath)

	return cachePath, "", nil // cached, no temp file to clean up
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
