package ramune

import (
	"encoding/json"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
)

// goOsHostname returns the system hostname.
func goOsHostname(args []any) (any, error) {
	h, err := os.Hostname()
	if err != nil {
		return "", err
	}
	return h, nil
}

// goOsUserInfo returns JSON with username, homedir, shell, uid, gid.
func goOsUserInfo(args []any) (any, error) {
	home, _ := os.UserHomeDir()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	shell := os.Getenv("SHELL")
	result := map[string]any{
		"username": username,
		"homedir":  home,
		"shell":    shell,
		"uid":      -1,
		"gid":      -1,
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// goUrlParse parses a URL and returns JSON with protocol, hostname, host, port,
// pathname, search, query, hash, href, and auth fields.
func goUrlParse(args []any) (any, error) {
	if len(args) < 1 {
		return "{}", nil
	}
	rawURL, _ := args[0].(string)
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return "{}", nil
	}
	port := u.Port()
	hostname := u.Hostname()
	result := map[string]any{
		"protocol": u.Scheme + ":",
		"hostname": hostname,
		"host":     u.Host,
		"port":     port,
		"pathname": u.Path,
		"search":   "",
		"query":    u.RawQuery,
		"hash":     "",
		"href":     rawURL,
		"auth":     "",
	}
	if u.RawQuery != "" {
		result["search"] = "?" + u.RawQuery
	}
	if u.Fragment != "" {
		result["hash"] = "#" + u.Fragment
	}
	if u.User != nil {
		pw, _ := u.User.Password()
		if pw != "" {
			result["auth"] = u.User.Username() + ":" + pw
		} else {
			result["auth"] = u.User.Username()
		}
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// goSpawnSync implements child_process.spawnSync.
// args: [command, argsJSON, optionsJSON]
// Returns JSON: {status, stdout, stderr, error}
func goSpawnSync(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("spawnSync: command required")
	}
	command, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("spawnSync: command must be string")
	}

	var cmdArgs []string
	if len(args) > 1 {
		if s, ok := args[1].(string); ok && s != "" {
			json.Unmarshal([]byte(s), &cmdArgs)
		}
	}

	var opts struct {
		Cwd   string            `json:"cwd"`
		Env   map[string]string `json:"env"`
		Input string            `json:"input"`
	}
	if len(args) > 2 {
		if s, ok := args[2].(string); ok && s != "" {
			json.Unmarshal([]byte(s), &opts)
		}
	}

	cmd := exec.Command(command, cmdArgs...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if opts.Env != nil {
		env := os.Environ()
		for k, v := range opts.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	if opts.Input != "" {
		cmd.Stdin = strings.NewReader(opts.Input)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	errMsg := ""
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			errMsg = err.Error()
		}
	}

	result := map[string]any{
		"status": exitCode,
		"stdout": stdout.String(),
		"stderr": stderr.String(),
		"error":  errMsg,
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// goExecSync implements child_process.execSync.
// args: [command, optionsJSON]
// Returns stdout string or error.
func goExecSync(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("execSync: command required")
	}
	command, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("execSync: command must be string")
	}

	var opts struct {
		Cwd string            `json:"cwd"`
		Env map[string]string `json:"env"`
	}
	if len(args) > 1 {
		if s, ok := args[1].(string); ok && s != "" {
			json.Unmarshal([]byte(s), &opts)
		}
	}

	cmd := exec.Command("sh", "-c", command)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if len(opts.Env) > 0 {
		env := make([]string, 0, len(opts.Env))
		for k, v := range opts.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

// goSpawnAsync implements child_process.spawn with stdin/stdout streaming.
// args: [command, argsJSON, optionsJSON, inputDataJSON]
// The process is started, input is written to stdin, and all stdout/stderr
// is collected. Returns JSON with stdout lines for event replay.
func goSpawnAsync(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("spawn: command required")
	}
	command, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("spawn: command must be string")
	}

	var cmdArgs []string
	if len(args) > 1 {
		if s, ok := args[1].(string); ok && s != "" {
			json.Unmarshal([]byte(s), &cmdArgs)
		}
	}

	var opts struct {
		Cwd   string            `json:"cwd"`
		Env   map[string]string `json:"env"`
		Input string            `json:"input"`
	}
	if len(args) > 2 {
		if s, ok := args[2].(string); ok && s != "" {
			json.Unmarshal([]byte(s), &opts)
		}
	}
	// Input data (written to stdin).
	if len(args) > 3 {
		if s, ok := args[3].(string); ok {
			opts.Input = s
		}
	}

	cmd := exec.Command(command, cmdArgs...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if opts.Env != nil {
		env := os.Environ()
		for k, v := range opts.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Write input and close stdin.
	if opts.Input != "" {
		io.WriteString(stdin, opts.Input)
	}
	stdin.Close()

	// Read all stdout.
	stdoutBytes, _ := io.ReadAll(stdoutPipe)
	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	result := map[string]any{
		"status": exitCode,
		"stdout": string(stdoutBytes),
		"stderr": stderr.String(),
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// goExecFileSync implements child_process.execFileSync.
// args: [file, argsJSON, optionsJSON]
// Returns stdout string or error.
func goExecFileSync(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("execFileSync: file required")
	}
	file, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("execFileSync: file must be string")
	}
	var cmdArgs []string
	if len(args) > 1 {
		if s, ok := args[1].(string); ok && s != "" {
			json.Unmarshal([]byte(s), &cmdArgs)
		}
	}
	var opts struct {
		Cwd string `json:"cwd"`
	}
	if len(args) > 2 {
		if s, ok := args[2].(string); ok && s != "" {
			json.Unmarshal([]byte(s), &opts)
		}
	}
	cmd := exec.Command(file, cmdArgs...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

// guessPlatform returns the current OS name (matching Node.js process.platform).
func guessPlatform() string {
	return goruntime.GOOS
}

// guessArch returns the current architecture name (matching Node.js process.arch).
func guessArch() string {
	// Map Go arch names to Node.js arch names.
	switch goruntime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goruntime.GOARCH // arm64, arm, etc. match Node.js
	}
}
