package ramune

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// asyncProcess represents a running subprocess managed by the Runtime.
type asyncProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu       sync.Mutex
	events   []processEvent // pending events to deliver to JS
	exited   bool
	exitCode int
}

type processEvent struct {
	Kind string // "stdout", "stderr", "exit", "error"
	Data string
	Code int
}

// processManager tracks all async processes for a Runtime.
type processManager struct {
	mu        sync.Mutex
	processes map[int]*asyncProcess
	nextID    int
	wakeFn    func() // wakes event loop when events arrive
}

func newProcessManager() *processManager {
	return &processManager{
		processes: make(map[int]*asyncProcess),
		nextID:    1,
	}
}

// spawnProcess starts a new async subprocess.
// If the command is `node` running a JS file, it uses ramune's own engine.
func (pm *processManager) spawnProcess(command string, args []string, cwd string, env map[string]string) (int, error) {
	// Intercept `node` commands — try running in ramune first, fall back to exec
	if scriptPath, scriptArgs, ok := isNodeCommand(command, args); ok {
		id, err := pm.spawnNodeInProcess(scriptPath, scriptArgs, cwd, env)
		if err == nil {
			return id, nil
		}
		// Fall back to external node
	}

	cmd := exec.Command(command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if env != nil {
		cmdEnv := os.Environ()
		for k, v := range env {
			cmdEnv = append(cmdEnv, k+"="+v)
		}
		cmd.Env = cmdEnv
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, err
	}

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	proc := &asyncProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}

	pm.mu.Lock()
	id := pm.nextID
	pm.nextID++
	pm.processes[id] = proc
	pm.mu.Unlock()

	// Wait for stdout/stderr readers to finish before signaling exit.
	var ioWg sync.WaitGroup
	ioWg.Add(2)

	// Read stdout in background goroutine.
	go func() {
		defer ioWg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
		for scanner.Scan() {
			line := scanner.Text()
			proc.mu.Lock()
			proc.events = append(proc.events, processEvent{Kind: "stdout", Data: line + "\n"})
			proc.mu.Unlock()
			if pm.wakeFn != nil {
				pm.wakeFn()
			}
		}
	}()

	// Read stderr in background goroutine.
	go func() {
		defer ioWg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			proc.mu.Lock()
			proc.events = append(proc.events, processEvent{Kind: "stderr", Data: line + "\n"})
			proc.mu.Unlock()
			if pm.wakeFn != nil {
				pm.wakeFn()
			}
		}
	}()

	// Wait for process exit in background goroutine.
	go func() {
		err := cmd.Wait()
		ioWg.Wait() // ensure all stdout/stderr is read before signaling exit
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		proc.mu.Lock()
		proc.exited = true
		proc.exitCode = exitCode
		proc.events = append(proc.events, processEvent{Kind: "exit", Code: exitCode})
		proc.mu.Unlock()
		if pm.wakeFn != nil {
			pm.wakeFn()
		}
	}()

	return id, nil
}

// drainEvents returns and clears pending events for a process.
func (pm *processManager) drainEvents(id int) []processEvent {
	pm.mu.Lock()
	proc, ok := pm.processes[id]
	pm.mu.Unlock()
	if !ok {
		return nil
	}

	proc.mu.Lock()
	events := proc.events
	proc.events = nil
	exited := proc.exited
	proc.mu.Unlock()

	// Clean up finished processes.
	if exited && len(events) > 0 {
		// Check if exit event is in this batch — clean up after delivery.
		for _, e := range events {
			if e.Kind == "exit" {
				pm.mu.Lock()
				delete(pm.processes, id)
				pm.mu.Unlock()
				break
			}
		}
	}

	return events
}

// writeStdin writes data to the process's stdin.
func (pm *processManager) writeStdin(id int, data string) error {
	pm.mu.Lock()
	proc, ok := pm.processes[id]
	pm.mu.Unlock()
	if !ok {
		return fmt.Errorf("process %d not found", id)
	}
	_, err := io.WriteString(proc.stdin, data)
	return err
}

// closeStdin closes the process's stdin.
func (pm *processManager) closeStdin(id int) error {
	pm.mu.Lock()
	proc, ok := pm.processes[id]
	pm.mu.Unlock()
	if !ok {
		return fmt.Errorf("process %d not found", id)
	}
	return proc.stdin.Close()
}

// killProcess kills the process.
func (pm *processManager) killProcess(id int) error {
	pm.mu.Lock()
	proc, ok := pm.processes[id]
	pm.mu.Unlock()
	if !ok {
		return nil
	}
	return proc.cmd.Process.Kill()
}

// hasActive returns true if any process is still running.
func (pm *processManager) hasActive() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return len(pm.processes) > 0
}

// spawnNodeInProcess runs a node script using ramune's engine instead of exec.
func (pm *processManager) spawnNodeInProcess(scriptPath string, scriptArgs []string, cwd string, env map[string]string) (int, error) {
	nr, err := spawnNodeRunner(scriptPath, scriptArgs, cwd, env)
	if err != nil {
		return 0, fmt.Errorf("node runner: %w", err)
	}

	proc := &asyncProcess{
		stdin:  nr.stdin,
		stdout: nr.stdout,
		stderr: nr.stderr,
	}

	pm.mu.Lock()
	id := pm.nextID
	pm.nextID++
	pm.processes[id] = proc
	pm.mu.Unlock()

	var ioWg sync.WaitGroup
	ioWg.Add(2)

	go func() {
		defer ioWg.Done()
		scanner := bufio.NewScanner(nr.stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			proc.mu.Lock()
			proc.events = append(proc.events, processEvent{Kind: "stdout", Data: line + "\n"})
			proc.mu.Unlock()
			if pm.wakeFn != nil {
				pm.wakeFn()
			}
		}
	}()

	go func() {
		defer ioWg.Done()
		scanner := bufio.NewScanner(nr.stderr)
		for scanner.Scan() {
			line := scanner.Text()
			proc.mu.Lock()
			proc.events = append(proc.events, processEvent{Kind: "stderr", Data: line + "\n"})
			proc.mu.Unlock()
			if pm.wakeFn != nil {
				pm.wakeFn()
			}
		}
	}()

	// Wait for the node runner to finish
	go func() {
		// Poll until the runner exits
		for {
			nr.mu.Lock()
			exited := nr.exited
			exitCode := nr.exitCode
			nr.mu.Unlock()
			if exited {
				nr.Close()
				ioWg.Wait()
				proc.mu.Lock()
				proc.exited = true
				proc.exitCode = exitCode
				proc.events = append(proc.events, processEvent{Kind: "exit", Code: exitCode})
				proc.mu.Unlock()
				if pm.wakeFn != nil {
					pm.wakeFn()
				}
				return
			}
			// Small sleep to avoid busy loop
			select {
			case <-make(chan struct{}):
			default:
			}
		}
	}()

	return id, nil
}

// processEvents drains events from all processes and delivers them to JS.
// Must be called on the dedicated JSC goroutine.
func (pm *processManager) processEvents(r *Runtime) {
	if pm == nil {
		return
	}
	pm.mu.Lock()
	if len(pm.processes) == 0 {
		pm.mu.Unlock()
		return
	}
	// Collect events from all processes.
	type pidEvents struct {
		id     int
		events []processEvent
	}
	var all []pidEvents
	var exited []int
	for id, proc := range pm.processes {
		proc.mu.Lock()
		if len(proc.events) > 0 {
			all = append(all, pidEvents{id, proc.events})
			proc.events = nil
			if proc.exited {
				exited = append(exited, id)
			}
		}
		proc.mu.Unlock()
	}
	for _, id := range exited {
		delete(pm.processes, id)
	}
	pm.mu.Unlock()

	if len(all) == 0 {
		return
	}

	// Deliver events to JS via __processDeliverEvents.
	evMap := make(map[string][]map[string]any, len(all))
	for _, pe := range all {
		key := itoa(pe.id)
		evs := make([]map[string]any, len(pe.events))
		for i, e := range pe.events {
			evs[i] = map[string]any{"Kind": e.Kind, "Data": e.Data, "Code": e.Code}
		}
		evMap[key] = evs
	}
	data, _ := json.Marshal(evMap)
	r.execLocked("if(typeof __processDeliverEvents==='function')__processDeliverEvents(" + string(data) + ")")
}

// --- Go callbacks for JS ---

func goAsyncSpawn(pm *processManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("spawn: command required")
		}
		command, _ := args[0].(string)

		var cmdArgs []string
		if len(args) > 1 {
			if s, ok := args[1].(string); ok && s != "" {
				json.Unmarshal([]byte(s), &cmdArgs)
			}
		}

		var opts struct {
			Cwd string            `json:"cwd"`
			Env map[string]string `json:"env"`
		}
		if len(args) > 2 {
			if s, ok := args[2].(string); ok && s != "" {
				json.Unmarshal([]byte(s), &opts)
			}
		}

		id, err := pm.spawnProcess(command, cmdArgs, opts.Cwd, opts.Env)
		if err != nil {
			return nil, err
		}
		return float64(id), nil
	}
}

func goAsyncSpawnWrite(pm *processManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("write: pid and data required")
		}
		id, _ := args[0].(float64)
		data, _ := args[1].(string)
		return nil, pm.writeStdin(int(id), data)
	}
}

func goAsyncSpawnCloseStdin(pm *processManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("closeStdin: pid required")
		}
		id, _ := args[0].(float64)
		return nil, pm.closeStdin(int(id))
	}
}

func goAsyncSpawnKill(pm *processManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("kill: pid required")
		}
		id, _ := args[0].(float64)
		return nil, pm.killProcess(int(id))
	}
}

func goAsyncSpawnDrain(pm *processManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return "[]", nil
		}
		id, _ := args[0].(float64)
		events := pm.drainEvents(int(id))
		if len(events) == 0 {
			return "[]", nil
		}
		out, _ := json.Marshal(events)
		return string(out), nil
	}
}

func goAsyncSpawnHasActive(pm *processManager) GoFunc {
	return func(args []any) (any, error) {
		return pm.hasActive(), nil
	}
}

// installAsyncSpawn registers the async spawn callbacks.
// Must be called with rt.mu held.
func (r *Runtime) installAsyncSpawn() error {
	pm := newProcessManager()
	pm.wakeFn = r.Wake
	r.procMgr = pm

	if err := r.registerFuncLocked("__go_async_spawn", goAsyncSpawn(pm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_async_spawn_write", goAsyncSpawnWrite(pm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_async_spawn_close_stdin", goAsyncSpawnCloseStdin(pm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_async_spawn_kill", goAsyncSpawnKill(pm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_async_spawn_drain", goAsyncSpawnDrain(pm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_async_spawn_has_active", goAsyncSpawnHasActive(pm)); err != nil {
		return err
	}

	return r.execLocked(asyncSpawnJSSource())
}

func asyncSpawnJSSource() string {
	return strings.TrimSpace(`
(function() {
	// Override spawn to use async process management.
	var cp = globalThis.require('child_process');
	var EventEmitter = globalThis.require('events').EventEmitter;

	var origSpawn = cp.spawn;
	cp.spawn = function(cmd, args, opts) {
		var argsJSON = args ? JSON.stringify(args) : '[]';
		var optsJSON = opts ? JSON.stringify({cwd: opts.cwd || '', env: opts.env || null}) : '{}';

		var pid;
		try {
			pid = __go_async_spawn(cmd, argsJSON, optsJSON);
		} catch(e) {
			// Fall back to deferred sync spawn if async spawn fails.
			if (typeof console !== 'undefined' && console.error) {
				console.error('[ramune] async spawn failed for ' + cmd + ': ' + e.message + ', falling back to sync');
			}
			return origSpawn(cmd, args, opts);
		}

		var child = new EventEmitter();
		child.pid = pid;
		child.killed = false;
		child.exitCode = null;
		child.connected = true;

		child.stdout = new EventEmitter();
		child.stdout.setEncoding = function() { return this; };
		child.stdout.pipe = function(dest) {
			child.stdout.on('data', function(chunk) { dest.write(chunk); });
			child.stdout.on('end', function() { dest.end(); });
			return dest;
		};

		child.stderr = new EventEmitter();
		child.stderr.setEncoding = function() { return this; };

		child.stdin = new EventEmitter();
		child.stdin.writable = true;
		child.stdin.write = function(data) {
			try { __go_async_spawn_write(pid, String(data)); } catch(e) {}
			return true;
		};
		child.stdin.end = function(data) {
			if (data) child.stdin.write(data);
			try { __go_async_spawn_close_stdin(pid); } catch(e) {}
		};

		child.kill = function(signal) {
			child.killed = true;
			try { __go_async_spawn_kill(pid); } catch(e) {}
		};
		child.off = child.removeListener;
		child.ref = function() { return child; };
		child.unref = function() { return child; };

		// Register in process registry for event delivery.
		__activeProcesses[String(pid)] = child;
		return child;
	};

	// Registry of active child processes by pid.
	var __activeProcesses = {};

	// Called by Go during event loop tick to deliver events.
	globalThis.__processDeliverEvents = function(eventsMap) {
		var pids = Object.keys(eventsMap);
		for (var p = 0; p < pids.length; p++) {
			var pid = pids[p];
			var child = __activeProcesses[pid];
			if (!child) continue;
			var events = eventsMap[pid];
			for (var i = 0; i < events.length; i++) {
				var ev = events[i];
				if (ev.Kind === 'stdout') {
					child.stdout.emit('data', ev.Data);
				} else if (ev.Kind === 'stderr') {
					child.stderr.emit('data', ev.Data);
				} else if (ev.Kind === 'exit') {
					child.exitCode = ev.Code;
					child.stdout.emit('end');
					child.stderr.emit('end');
					child.emit('close', ev.Code);
					child.emit('exit', ev.Code);
					delete __activeProcesses[pid];
				} else if (ev.Kind === 'error') {
					child.emit('error', new Error(ev.Data));
					delete __activeProcesses[pid];
				}
			}
		}
	};
})();
`)
}
