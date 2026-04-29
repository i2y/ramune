package ramune

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"
)

// tuiSession owns one BubbleTea program backed by JS update/view callbacks.
// Update/View calls happen on the BubbleTea goroutine and reach JS through
// JSFunc.Call (which dispatches onto the JSC thread). The JSC thread must
// stay tickable while the program runs, so installTUI registers a
// TickManager whose HasActive() pins the event loop until the program
// exits.
type tuiSession struct {
	rt     *Runtime
	id     uint64
	prog   *tea.Program
	update *JSFunc
	view   *JSFunc
	done   *JSFunc

	// stateRaw is the JSON-encoded model that JS-side returned from the
	// last update. We keep it as a string so the BubbleTea goroutine can
	// pass it back to JS unchanged on the next update tick — JS owns
	// model semantics; Go only routes.
	stateRaw atomic.Value // string

	// Headless plumbing — when set, the program runs with the pipe as
	// stdin and outBuf as stdout, so tests can drive it without a real
	// TTY. closeInput unblocks the reader on shutdown.
	closeInput func()
	outBuf     *bytes.Buffer

	mu     sync.Mutex
	exited bool
}

type tuiManager struct {
	mu       sync.Mutex
	sessions map[uint64]*tuiSession
	servers  map[uint64]*tuiSSHServer
	next     uint64
	// pending carries "session done" events from BubbleTea worker
	// goroutines back to the JSC thread. Calling doneFn.Call from a
	// worker is racy with Runtime.Close — Close drains the JSC loop
	// then tears down libJavaScriptCore state, while the worker may
	// still have a JSFunc dispatch in flight, which deref's freed JSC
	// memory and SEGVs. ProcessEvents drains this queue inside the
	// JSC tick so the JS-side resolve runs strictly before HasActive
	// flips false.
	pending []tuiDoneEvent
}

type tuiSSHServer struct {
	srv  *ssh.Server
	done *JSFunc
}

type tuiDoneEvent struct {
	// done is the JS callback to resolve. sess may be nil for events
	// that don't belong to a per-connection session (e.g. SSH server
	// shutdown) — only when sess is set do we mark it exited and
	// remove it from the manager's session map.
	done       *JSFunc
	sess       *tuiSession
	err        string
	finalState string
	captured   string
}

func (m *tuiManager) ProcessEvents(r *Runtime) {
	m.mu.Lock()
	pending := m.pending
	m.pending = nil
	m.mu.Unlock()
	for _, ev := range pending {
		var errArg any
		if ev.err != "" {
			errArg = ev.err
		}
		_, _ = ev.done.Call(errArg, ev.finalState, ev.captured)
		// Mark the session exited only after the Promise resolves so
		// HasActive stays true until the JS-side .then has been
		// queued — otherwise the event loop bails before the user's
		// continuation runs. Server-only events have sess == nil.
		if ev.sess != nil {
			ev.sess.mu.Lock()
			ev.sess.exited = true
			ev.sess.mu.Unlock()
			m.mu.Lock()
			delete(m.sessions, ev.sess.id)
			m.mu.Unlock()
		}
	}
}

func (m *tuiManager) HasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) > 0 || len(m.servers) > 0 {
		return true
	}
	for _, s := range m.sessions {
		s.mu.Lock()
		live := !s.exited
		s.mu.Unlock()
		if live {
			return true
		}
	}
	return false
}

// goTUIServeSSH starts a wish SSH server that runs a Bubbletea program
// per connection, sharing init/update/view JSFuncs across connections
// while keeping each connection's state isolated. Returns the server
// id; the JS side gets the resolution via doneFn when the server stops
// (Close, listener error, or stop_ssh).
func goTUIServeSSH(rt *Runtime, mgr *tuiManager) func([]any) (any, error) {
	return func(args []any) (any, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("tui.serveSSH: need (addr, hostKeyPath, initFn, updateFn, viewFn, doneFn)")
		}
		addr, _ := args[0].(string)
		hostKeyPath, _ := args[1].(string)
		initFn, ok := args[2].(*JSFunc)
		if !ok {
			return nil, fmt.Errorf("tui.serveSSH: init must be a function")
		}
		updateFn, ok := args[3].(*JSFunc)
		if !ok {
			return nil, fmt.Errorf("tui.serveSSH: update must be a function")
		}
		viewFn, ok := args[4].(*JSFunc)
		if !ok {
			return nil, fmt.Errorf("tui.serveSSH: view must be a function")
		}
		doneFn, ok := args[5].(*JSFunc)
		if !ok {
			return nil, fmt.Errorf("tui.serveSSH: done must be a function")
		}
		if addr == "" {
			addr = ":2222"
		}

		mgr.mu.Lock()
		mgr.next++
		id := mgr.next
		mgr.mu.Unlock()

		// Per-connection program handler. Each SSH session gets its
		// own tuiSession with isolated state, plumbed to the wire via
		// MakeOptions (WithInput/WithOutput pinned to ssh.Session).
		handler := func(s ssh.Session) *tea.Program {
			initJSON, callErr := initFn.Call()
			if callErr != nil {
				_, _ = fmt.Fprintf(s.Stderr(), "ramune.tui: init() error: %v\r\n", callErr)
				return nil
			}
			initStr, _ := initJSON.(string)
			if initStr == "" {
				initStr = "null"
			}
			sess := &tuiSession{rt: rt, update: updateFn, view: viewFn, done: doneFn}
			sess.stateRaw.Store(initStr)
			progOpts := append(bm.MakeOptions(s),
				tea.WithoutSignalHandler(),
				tea.WithoutCatchPanics(),
			)
			return tea.NewProgram(tuiModel{sess: sess}, progOpts...)
		}

		srvOpts := []ssh.Option{
			wish.WithAddress(addr),
			wish.WithMiddleware(
				bm.MiddlewareWithProgramHandler(handler, termenv.ANSI256),
			),
		}
		if hostKeyPath != "" {
			srvOpts = append(srvOpts, wish.WithHostKeyPath(hostKeyPath))
		}
		srv, err := wish.NewServer(srvOpts...)
		if err != nil {
			return nil, fmt.Errorf("tui.serveSSH: %w", err)
		}
		ent := &tuiSSHServer{srv: srv, done: doneFn}
		mgr.mu.Lock()
		mgr.servers[id] = ent
		mgr.mu.Unlock()

		go func() {
			err := srv.ListenAndServe()
			mgr.mu.Lock()
			delete(mgr.servers, id)
			var errStr string
			if err != nil && err != ssh.ErrServerClosed {
				errStr = err.Error()
			}
			mgr.pending = append(mgr.pending, tuiDoneEvent{
				done: doneFn,
				err:  errStr,
			})
			mgr.mu.Unlock()
			rt.Wake()
		}()

		return float64(id), nil
	}
}

// goTUIStopSSH closes a running SSH server by id. The server's
// ListenAndServe goroutine then queues the done event normally.
func goTUIStopSSH(mgr *tuiManager) func([]any) (any, error) {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, nil
		}
		id, _ := args[0].(float64)
		mgr.mu.Lock()
		sv, ok := mgr.servers[uint64(id)]
		mgr.mu.Unlock()
		if ok && sv != nil {
			_ = sv.srv.Close()
		}
		return nil, nil
	}
}

func (m *tuiManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.prog != nil {
			s.prog.Kill()
		}
	}
	for _, sv := range m.servers {
		_ = sv.srv.Close()
	}
	m.sessions = nil
	m.servers = nil
}

// tuiModel is BubbleTea's view of the world. It carries a session
// reference and the current JS-encoded state. Update calls JS, replaces
// the state, and asks for a Cmd; View calls JS to render.
type tuiModel struct {
	sess *tuiSession
}

func (m tuiModel) Init() tea.Cmd { return nil }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Translate the BubbleTea msg into a JS-shaped object so userland
	// can pattern-match on `msg.type`. Only key + window-resize + quit
	// are wired today; expanding the surface is a one-liner per case.
	jsMsg := tuiMsgToJS(msg)
	if jsMsg == nil {
		return m, nil
	}
	curr, _ := m.sess.stateRaw.Load().(string)
	res, err := m.sess.update.Call(curr, jsMsg)
	if err != nil {
		return m, tea.Quit
	}
	// JS update returns either a state value or { state, cmd }. Treat
	// the return as state when it's not a tagged envelope.
	newState, cmd := decomposeUpdateResult(res)
	if newState != "" {
		m.sess.stateRaw.Store(newState)
	}
	return m, cmd
}

func (m tuiModel) View() string {
	curr, _ := m.sess.stateRaw.Load().(string)
	res, err := m.sess.view.Call(curr)
	if err != nil {
		return fmt.Sprintf("ramune.tui: view error: %v", err)
	}
	if s, ok := res.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", res)
}

func tuiMsgToJS(msg tea.Msg) any {
	switch m := msg.(type) {
	case tea.KeyMsg:
		return map[string]any{
			"type":  "key",
			"key":   m.String(),
			"alt":   m.Alt,
			"runes": string(m.Runes),
		}
	case tea.WindowSizeMsg:
		return map[string]any{
			"type":   "resize",
			"width":  float64(m.Width),
			"height": float64(m.Height),
		}
	case tea.MouseMsg:
		return map[string]any{
			"type":   "mouse",
			"x":      float64(m.X),
			"y":      float64(m.Y),
			"button": int(m.Type),
			"alt":    m.Alt,
			"ctrl":   m.Ctrl,
			"shift":  m.Shift,
		}
	case tea.QuitMsg:
		return map[string]any{"type": "quit"}
	case tuiDispatchMsg:
		// User-injected via Ramune.tui.dispatch(payload). The payload
		// is whatever JS handed in; we don't wrap further so user
		// code can pattern-match on payload.type the same way it
		// does for built-in events.
		return m.payload
	}
	return nil
}

// decomposeUpdateResult takes whatever JS returned from update() and
// teases out (newStateJSON, cmd). Three accepted shapes:
//
//	"…json…"                 → state-only, no cmd
//	{ state: …, cmd: "quit" } → tagged envelope, only "quit" cmd today
//	any other                → re-encode as state
func decomposeUpdateResult(res any) (string, tea.Cmd) {
	switch r := res.(type) {
	case string:
		return r, nil
	case map[string]any:
		if cmd, ok := r["cmd"].(string); ok && cmd == "quit" {
			if state, ok := r["state"]; ok {
				if b, err := json.Marshal(state); err == nil {
					return string(b), tea.Quit
				}
			}
			return "", tea.Quit
		}
		// Untagged map → treat the whole map as state.
		if b, err := json.Marshal(r); err == nil {
			return string(b), nil
		}
	default:
		if b, err := json.Marshal(res); err == nil {
			return string(b), nil
		}
	}
	return "", nil
}

func (r *Runtime) installTUI() error {
	mgr := &tuiManager{
		sessions: map[uint64]*tuiSession{},
		servers:  map[uint64]*tuiSSHServer{},
	}
	r.customTickMgrs = append(r.customTickMgrs, mgr)

	if err := r.registerFuncLocked("__go_tui_start", goTUIStart(r, mgr)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_tui_style", goTUIStyle); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_tui_quit", goTUIQuit(mgr)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_tui_dispatch", goTUIDispatch(mgr)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_tui_markdown", goTUIMarkdown); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_tui_serve_ssh", goTUIServeSSH(r, mgr)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_tui_stop_ssh", goTUIStopSSH(mgr)); err != nil {
		return err
	}

	return r.execLocked(`(function() {
	globalThis.Ramune.tui = {
		run: function(opts) {
			opts = opts || {};
			var initFn = opts.init || function() { return {}; };
			var updateFn = opts.update || function(s) { return s; };
			var viewFn = opts.view || function() { return ''; };
			var initial = initFn();
			var initialJSON = JSON.stringify(initial);
			var runOpts = {
				fullscreen: !!opts.fullscreen,
				mouse: !!opts.mouse,
				headless: !!opts.headless,
			};
			return new Promise(function(resolve, reject) {
				__go_tui_start(
					initialJSON,
					function(stateJSON, msg) {
						var state = JSON.parse(stateJSON);
						var next = updateFn(state, msg);
						if (next && typeof next === 'object' && '_state' in next) {
							return { state: next._state, cmd: next._cmd || null };
						}
						return JSON.stringify(next === undefined ? state : next);
					},
					function(stateJSON) {
						var state = JSON.parse(stateJSON);
						var out = viewFn(state);
						return out == null ? '' : String(out);
					},
					function(err, finalStateJSON, captured) {
						if (err) reject(new Error(err));
						else resolve({
							state: finalStateJSON ? JSON.parse(finalStateJSON) : null,
							output: captured || '',
						});
					},
					runOpts
				);
			}).then(function(envelope) {
				// Backwards-compatible default: returns just the
				// state. Headless callers want both — if opts.headless
				// is true, hand back the envelope verbatim so tests can
				// inspect captured stdout.
				return runOpts.headless ? envelope : envelope.state;
			});
		},
		quit: function(sessionId) {
			__go_tui_quit(sessionId || 0);
		},
		style: function(text, opts) {
			return __go_tui_style(String(text), opts || {});
		},
		// Builder mirroring Lipgloss's chainable Style API. Stays as a
		// JS object literal so users can compose styles before passing
		// to style().
		Cmd: {
			quit: function(state) { return { _state: state, _cmd: 'quit' }; },
			// delay(ms, msg) → schedules a single dispatch after ms.
			// Returns a token usable with cancel().
			delay: function(ms, msg) {
				return setTimeout(function() {
					globalThis.Ramune.tui.dispatch(msg);
				}, ms);
			},
			cancelDelay: function(token) { clearTimeout(token); },
			// every(ms, msg) → recurring dispatch every ms. msg can be
			// a value or a () => value factory. Returns the interval
			// token; pass to cancelEvery to stop.
			every: function(ms, msg) {
				return setInterval(function() {
					globalThis.Ramune.tui.dispatch(typeof msg === 'function' ? msg() : msg);
				}, ms);
			},
			cancelEvery: function(token) { clearInterval(token); },
			// fromPromise(p, onResolve, onReject) → dispatches the
			// result of one of the two factories when the Promise
			// settles. Either factory can be omitted; if onReject is
			// missing, rejection is silently dropped.
			fromPromise: function(p, onResolve, onReject) {
				p.then(function(v) {
					if (typeof onResolve === 'function') {
						globalThis.Ramune.tui.dispatch(onResolve(v));
					}
				}, function(err) {
					if (typeof onReject === 'function') {
						globalThis.Ramune.tui.dispatch(onReject(err));
					}
				});
			},
		},
		dispatch: function(msg) { __go_tui_dispatch(msg); },
		markdown: function(text, opts) { return __go_tui_markdown(String(text), opts || {}); },
		// serveSSH({addr, hostKeyPath?, init, update, view}) starts a
		// wish-backed SSH server. Each incoming connection runs a
		// fresh Bubbletea program backed by per-connection state but
		// shared init/update/view callbacks. Resolves when the
		// server stops (Close, ListenAndServe error). hostKeyPath is
		// recommended for stable host fingerprints; omit during dev
		// and wish auto-generates an ephemeral key in CWD.
		serveSSH: function(opts) {
			opts = opts || {};
			var initFn = opts.init || function() { return {}; };
			var updateFn = opts.update || function(s) { return s; };
			var viewFn = opts.view || function() { return ''; };
			return new Promise(function(resolve, reject) {
				var id;
				id = __go_tui_serve_ssh(
					opts.addr || ':2222',
					opts.hostKeyPath || '',
					function() {
						return JSON.stringify(initFn());
					},
					function(stateJSON, msg) {
						var state = JSON.parse(stateJSON);
						var next = updateFn(state, msg);
						if (next && typeof next === 'object' && '_state' in next) {
							return { state: next._state, cmd: next._cmd || null };
						}
						return JSON.stringify(next === undefined ? state : next);
					},
					function(stateJSON) {
						var state = JSON.parse(stateJSON);
						var out = viewFn(state);
						return out == null ? '' : String(out);
					},
					function(err) {
						if (err) reject(new Error(err));
						else resolve();
					}
				);
				// Expose the id back so callers can stop the server
				// without retaining the Promise resolver.
				if (opts.onStart) opts.onStart(id);
			});
		},
		stopSSH: function(id) { __go_tui_stop_ssh(id); },
		// test({init, update, view, script}) drives the same shape
		// run() does but without tea.Program. Pure JS, deterministic,
		// no TTY required. Returns { frames, states, finalState }.
		// The script is an array of msgs. Update may return a tagged
		// _cmd: 'quit' envelope to stop early.
		test: function(opts) {
			opts = opts || {};
			var initFn = opts.init || function() { return {}; };
			var updateFn = opts.update || function(s) { return s; };
			var viewFn = opts.view || function() { return ''; };
			var script = opts.script || [];
			var states = [initFn()];
			var frames = [String(viewFn(states[0]))];
			var quit = false;
			for (var i = 0; i < script.length && !quit; i++) {
				var prev = states[states.length - 1];
				var next = updateFn(prev, script[i]);
				var ns = prev;
				if (next && typeof next === 'object' && '_state' in next) {
					ns = next._state;
					if (next._cmd === 'quit') quit = true;
				} else if (next !== undefined) {
					ns = next;
				}
				states.push(ns);
				frames.push(String(viewFn(ns)));
			}
			return {
				frames: frames,
				states: states,
				finalState: states[states.length - 1],
				quit: quit,
			};
		},
	};

	// JSX runtime: tsgo lowers <Tag prop="x">child</Tag> in .tsx files to
	// Ramune.tui.h(Tag, {prop:"x"}, child). Components are plain functions
	// returning strings; intrinsic strings ('box','text','stack','spacer')
	// also resolve to the matching builtin so users can stay HTML-shaped
	// if they prefer.
	// styleRest is the shared "render content, splice in remaining
	// props as Lipgloss style options" helper. Every component below
	// uses it to forward border / padding / fg / bg / etc. to the
	// outer Lipgloss wrapper while excluding its own consumed props.
	function styleRest(content, props, excludeKeys) {
		var rest = Object.assign({}, props);
		for (var i = 0; i < excludeKeys.length; i++) delete rest[excludeKeys[i]];
		return globalThis.Ramune.tui.style(content, rest);
	}
	function flattenChildren(children) {
		var out = [];
		for (var i = 0; i < children.length; i++) {
			var c = children[i];
			if (c == null || c === false || c === true) continue;
			if (Array.isArray(c)) {
				var inner = flattenChildren(c);
				for (var j = 0; j < inner.length; j++) out.push(inner[j]);
			} else {
				out.push(String(c));
			}
		}
		return out;
	}
	globalThis.Ramune.tui.h = function(component, props) {
		var children = [];
		for (var i = 2; i < arguments.length; i++) children.push(arguments[i]);
		var p = props || {};
		if (typeof component === 'function') {
			return component(p, flattenChildren(children));
		}
		// Intrinsic string component → look up a builtin.
		var builtin = globalThis.Ramune.tui[component];
		if (typeof builtin === 'function') return builtin(p, flattenChildren(children));
		throw new Error('ramune.tui: unknown component ' + String(component));
	};
	globalThis.Ramune.tui.Fragment = function(_props, children) {
		return children.join('');
	};
	globalThis.Ramune.tui.Text = function(props, children) {
		return globalThis.Ramune.tui.style(children.join(''), props);
	};
	globalThis.Ramune.tui.Box = function(props, children) {
		return globalThis.Ramune.tui.style(children.join('\n'), props);
	};
	globalThis.Ramune.tui.Stack = function(props, children) {
		var sep = props.gap > 0 ? '\n'.repeat(props.gap + 1) : '\n';
		return globalThis.Ramune.tui.style(children.join(sep), props);
	};
	globalThis.Ramune.tui.Row = function(props, children) {
		return globalThis.Ramune.tui.style(children.join(props.gap > 0 ? ' '.repeat(props.gap) : ' '), props);
	};
	globalThis.Ramune.tui.Spacer = function(props) {
		var n = (props && props.size) || 1;
		return '\n'.repeat(n);
	};

	// Markdown: glamour-powered terminal rendering. Children become the
	// markdown source; the content prop overrides children when set.
	// Defaults: theme=auto (detects dark/light bg), width=80.
	globalThis.Ramune.tui.Markdown = function(props, children) {
		var src = props.content != null ? String(props.content) : (children || []).join('');
		var rendered = globalThis.Ramune.tui.markdown(src, {
			theme: props.theme,
			width: props.width,
		});
		return styleRest(rendered, props, ['content', 'theme', 'width']);
	};

	// Spinner frame sets cribbed from Charm's bubbles/spinner. Index by
	// type, advance by frame index. Pure render — user owns the frame
	// counter and ticks it via Cmd.every.
	var spinnerFrames = {
		dot:    ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'],
		line:   ['|', '/', '-', '\\'],
		mini:   ['⠂', '⠄', '⡀', '⢀', '⠠', '⠐', '⠈'],
		jump:   ['⢄', '⢂', '⢁', '⡁', '⡈', '⡐', '⡠'],
		points: ['∙∙∙', '●∙∙', '∙●∙', '∙∙●'],
		moon:   ['🌑', '🌒', '🌓', '🌔', '🌕', '🌖', '🌗', '🌘'],
		ellip:  ['',   '.',  '..', '...'],
	};
	globalThis.Ramune.tui.Spinner = function(props) {
		var frames = spinnerFrames[props.type] || spinnerFrames.dot;
		var idx = ((props.frame || 0) % frames.length + frames.length) % frames.length;
		return globalThis.Ramune.tui.style(frames[idx], props);
	};
	globalThis.Ramune.tui.spinnerFrames = spinnerFrames;

	// List: receives items + selected index, renders each row through a
	// caller-supplied renderItem(item, isSelected) or a default cursor
	// prefix. Vertical viewport: maxRows clamps the visible window so
	// long lists scroll around the cursor.
	globalThis.Ramune.tui.List = function(props) {
		var items = props.items || [];
		var sel = Math.max(0, Math.min((props.selected || 0) | 0, items.length - 1));
		var maxRows = (props.maxRows | 0) || items.length;
		var renderItem = props.renderItem || function(item, isSelected) {
			return (isSelected ? '› ' : '  ') + String(item);
		};
		// Center selection in the viewport when possible.
		var half = Math.floor(maxRows / 2);
		var start = Math.max(0, Math.min(items.length - maxRows, sel - half));
		var end = Math.min(items.length, start + maxRows);
		var rows = [];
		for (var i = start; i < end; i++) {
			var line = renderItem(items[i], i === sel);
			if (i === sel && props.selectedStyle) {
				line = globalThis.Ramune.tui.style(line, props.selectedStyle);
			}
			rows.push(line);
		}
		// Top/bottom indicators when scrolled — small affordance, no
		// dependency on a particular Lipgloss border.
		if (start > 0) rows.unshift(globalThis.Ramune.tui.style('  …', { fg: '242' }));
		if (end < items.length) rows.push(globalThis.Ramune.tui.style('  …', { fg: '242' }));
		var inner = rows.join('\n');
		// Don't double-apply selected/renderItem props on the outer box.
		return styleRest(inner, props, ['items', 'selected', 'renderItem', 'maxRows', 'selectedStyle']);
	};

	// Input: stateless render of a value + caret, with optional
	// placeholder. Caller manages state and cursor position; the
	// helpers in Ramune.tui.input.* (below) keep update() terse.
	globalThis.Ramune.tui.Input = function(props) {
		var value = String(props.value == null ? '' : props.value);
		var placeholder = props.placeholder || '';
		var focused = props.focused !== false;
		var cursor = props.cursor;
		if (cursor == null) cursor = value.length;
		cursor = Math.max(0, Math.min(cursor, value.length));
		var rendered;
		if (value.length === 0) {
			rendered = focused
				? globalThis.Ramune.tui.style('▎', { fg: '12' }) +
				  globalThis.Ramune.tui.style(placeholder, { fg: '242' })
				: globalThis.Ramune.tui.style(placeholder, { fg: '242' });
		} else if (focused) {
			var before = value.slice(0, cursor);
			var at = value.slice(cursor, cursor + 1) || ' ';
			var after = value.slice(cursor + 1);
			rendered = before +
				globalThis.Ramune.tui.style(at, { bg: '12', fg: '0' }) +
				after;
		} else {
			rendered = value;
		}
		return styleRest(rendered, props, ['value', 'placeholder', 'focused', 'cursor']);
	};

	// Progress: bar with optional percent label. value is 0..1.
	// type: 'bar' (default) draws filled/empty cells; 'gradient' uses
	// half-cell granularity for smoother fill on supported fonts. The
	// fillStyle / emptyStyle props are applied to the respective
	// halves so themes can pick colors independent of the wrapping
	// border / padding.
	globalThis.Ramune.tui.Progress = function(props) {
		var v = Math.max(0, Math.min(1, +props.value || 0));
		var width = Math.max(1, (props.width | 0) || 40);
		var showPercent = props.showPercent !== false;
		var fillCh = props.fillChar || '█';
		var emptyCh = props.emptyChar || '░';
		var label = '';
		var barWidth = width;
		if (showPercent) {
			label = ' ' + Math.round(v * 100) + '%';
			barWidth = Math.max(1, width - label.length);
		}
		var filled = Math.round(v * barWidth);
		var fillStr = fillCh.repeat(filled);
		var emptyStr = emptyCh.repeat(barWidth - filled);
		if (props.fillStyle) fillStr = globalThis.Ramune.tui.style(fillStr, props.fillStyle);
		if (props.emptyStyle) emptyStr = globalThis.Ramune.tui.style(emptyStr, props.emptyStyle);
		var labelStr = label;
		if (showPercent && props.labelStyle) labelStr = globalThis.Ramune.tui.style(label, props.labelStyle);
		return styleRest(fillStr + emptyStr + labelStr, props, [
			'value', 'width', 'showPercent', 'fillChar', 'emptyChar',
			'fillStyle', 'emptyStyle', 'labelStyle',
		]);
	};

	// Viewport: clip + soft-scroll a long string. Splits on \n,
	// applies an offset, slices to height rows. width truncates each
	// row; pass 0 to skip width clipping (lipgloss may still wrap if
	// width is set on the outer style).
	globalThis.Ramune.tui.Viewport = function(props) {
		var content = String(props.content == null ? '' : props.content);
		var height = Math.max(1, (props.height | 0) || 10);
		var offset = Math.max(0, (props.offset | 0) || 0);
		var width = (props.width | 0) || 0;
		var lines = content.split('\n');
		var slice = lines.slice(offset, offset + height);
		if (width > 0) {
			slice = slice.map(function(row) {
				if (row.length <= width) return row;
				return row.slice(0, width - 1) + '…';
			});
		}
		// Pad to fixed height so the box doesn't shrink on short
		// content — keeps surrounding layout stable as content scrolls.
		while (slice.length < height) slice.push('');
		// Optional scroll indicator suffix on the last visible row.
		var atTop = offset === 0;
		var atBot = offset + height >= lines.length;
		if (props.scrollHint !== false && lines.length > height) {
			var hint = atTop ? ' ▼' : atBot ? ' ▲' : ' ↕';
			slice[slice.length - 1] =
				slice[slice.length - 1].replace(/\s+$/, '') + ' ' + globalThis.Ramune.tui.style(hint, { fg: '242' });
		}
		return styleRest(slice.join('\n'), props, [
			'content', 'offset', 'height', 'width', 'scrollHint',
		]);
	};

	// Tabs: horizontal label strip with the selected entry highlighted.
	// labels are strings; selected is the active index. Non-active
	// entries pick up tabStyle, the active one merges activeStyle on
	// top. separator joins entries.
	globalThis.Ramune.tui.Tabs = function(props) {
		var labels = props.labels || [];
		var sel = Math.max(0, Math.min((props.selected | 0) || 0, labels.length - 1));
		var sep = props.separator == null ? '  ' : props.separator;
		var tabStyle = props.tabStyle || { fg: '242', padding: [0, 1] };
		var activeStyle = props.activeStyle || { fg: '15', bg: '12', bold: true, padding: [0, 1] };
		var rendered = labels.map(function(label, i) {
			return globalThis.Ramune.tui.style(String(label), i === sel ? activeStyle : tabStyle);
		}).join(sep);
		return styleRest(rendered, props, [
			'labels', 'selected', 'separator', 'tabStyle', 'activeStyle',
		]);
	};

	// Help: keymap renderer mirroring bubbles/help. Two modes — short
	// (one row, key•desc•key•desc) and full (multi-row table). keys
	// is [{key, desc}, ...]; mode defaults to 'short'.
	globalThis.Ramune.tui.Help = function(props) {
		var keys = props.keys || [];
		var mode = props.mode || 'short';
		var keyStyle = props.keyStyle || { fg: '12', bold: true };
		var descStyle = props.descStyle || { fg: '242' };
		var sepStyle = props.sepStyle || { fg: '238' };
		var sep = globalThis.Ramune.tui.style(' • ', sepStyle);
		var rendered;
		if (mode === 'full') {
			// Two-column table: key | desc, padded to the longest key.
			var maxKey = keys.reduce(function(m, k) {
				return Math.max(m, String(k.key).length);
			}, 0);
			rendered = keys.map(function(k) {
				var key = globalThis.Ramune.tui.style(String(k.key).padEnd(maxKey, ' '), keyStyle);
				var desc = globalThis.Ramune.tui.style(String(k.desc || ''), descStyle);
				return key + '  ' + desc;
			}).join('\n');
		} else {
			rendered = keys.map(function(k) {
				return globalThis.Ramune.tui.style(String(k.key), keyStyle) +
					' ' +
					globalThis.Ramune.tui.style(String(k.desc || ''), descStyle);
			}).join(sep);
		}
		return styleRest(rendered, props, [
			'keys', 'mode', 'keyStyle', 'descStyle', 'sepStyle',
		]);
	};

	// Textarea: multi-line stateless input. value is the full string
	// (\n-separated). cursor is { row, col }. props.rows fixes the
	// visible height; long content scrolls so the cursor row stays
	// visible. props.cols truncates each row.
	globalThis.Ramune.tui.Textarea = function(props) {
		var value = String(props.value == null ? '' : props.value);
		var cursor = props.cursor || { row: 0, col: 0 };
		var focused = props.focused !== false;
		var rows = (props.rows | 0) || 0;
		var cols = (props.cols | 0) || 0;
		var lines = value.split('\n');
		// Clamp cursor to actual content.
		cursor = {
			row: Math.max(0, Math.min(cursor.row | 0, lines.length - 1)),
			col: Math.max(0, Math.min(cursor.col | 0, lines[Math.max(0, Math.min(cursor.row | 0, lines.length - 1))].length)),
		};
		// Soft-truncate each line to cols. Tracking truncation in the
		// cursor row for cursor placement is intentional simple — we
		// don't horizontally scroll within a row yet.
		if (cols > 0) {
			lines = lines.map(function(row, i) {
				if (row.length <= cols) return row;
				return row.slice(0, cols - 1) + '…';
			});
		}
		// Vertical scrolling: when rows is set and the cursor would
		// fall outside the window, slide the window so the cursor
		// stays visible. Center-on-scroll keeps the user oriented.
		var visibleStart = 0;
		var visibleLines = lines;
		if (rows > 0 && lines.length > rows) {
			var half = Math.floor(rows / 2);
			visibleStart = Math.max(0, Math.min(lines.length - rows, cursor.row - half));
			visibleLines = lines.slice(visibleStart, visibleStart + rows);
		}
		// Pad to fixed rows so the box doesn't shrink as content grows.
		if (rows > 0) {
			while (visibleLines.length < rows) visibleLines.push('');
		}
		// Place the caret. When focused and the cursor row is in the
		// visible window, replace the char at col with an inverse
		// half. Empty/end position renders an inverse space.
		if (focused) {
			var visibleCursorRow = cursor.row - visibleStart;
			if (visibleCursorRow >= 0 && visibleCursorRow < visibleLines.length) {
				var row = visibleLines[visibleCursorRow];
				var before = row.slice(0, cursor.col);
				var at = row.slice(cursor.col, cursor.col + 1) || ' ';
				var after = row.slice(cursor.col + 1);
				visibleLines[visibleCursorRow] = before +
					globalThis.Ramune.tui.style(at, { bg: '12', fg: '0' }) +
					after;
			}
		}
		return styleRest(visibleLines.join('\n'), props, [
			'value', 'cursor', 'focused', 'rows', 'cols',
		]);
	};
	globalThis.Ramune.tui.textarea = {
		init: function(value) {
			var v = value == null ? '' : String(value);
			var lines = v.split('\n');
			return {
				value: v,
				cursor: { row: lines.length - 1, col: lines[lines.length - 1].length },
			};
		},
		handleKey: function(state, msg) {
			if (!msg || msg.type !== 'key') return null;
			var s = state || { value: '', cursor: { row: 0, col: 0 } };
			var lines = s.value.split('\n');
			var r = s.cursor.row;
			var c = s.cursor.col;
			var key = msg.key;
			function rebuild(rows, nr, nc) {
				return { value: rows.join('\n'), cursor: { row: nr, col: nc } };
			}
			if (key === 'up') {
				if (r === 0) return s;
				var nr = r - 1;
				return rebuild(lines, nr, Math.min(c, lines[nr].length));
			}
			if (key === 'down') {
				if (r === lines.length - 1) return s;
				var nr2 = r + 1;
				return rebuild(lines, nr2, Math.min(c, lines[nr2].length));
			}
			if (key === 'left') {
				if (c > 0) return rebuild(lines, r, c - 1);
				if (r > 0) return rebuild(lines, r - 1, lines[r - 1].length);
				return s;
			}
			if (key === 'right') {
				if (c < lines[r].length) return rebuild(lines, r, c + 1);
				if (r < lines.length - 1) return rebuild(lines, r + 1, 0);
				return s;
			}
			if (key === 'home') return rebuild(lines, r, 0);
			if (key === 'end') return rebuild(lines, r, lines[r].length);
			if (key === 'enter') {
				// Split at cursor into two rows.
				var head = lines[r].slice(0, c);
				var tail = lines[r].slice(c);
				var newLines = lines.slice(0, r).concat([head, tail]).concat(lines.slice(r + 1));
				return rebuild(newLines, r + 1, 0);
			}
			if (key === 'backspace') {
				if (c > 0) {
					lines[r] = lines[r].slice(0, c - 1) + lines[r].slice(c);
					return rebuild(lines, r, c - 1);
				}
				if (r > 0) {
					var prevLen = lines[r - 1].length;
					var merged = lines[r - 1] + lines[r];
					var newLines2 = lines.slice(0, r - 1).concat([merged]).concat(lines.slice(r + 1));
					return rebuild(newLines2, r - 1, prevLen);
				}
				return s;
			}
			if (key === 'delete') {
				if (c < lines[r].length) {
					lines[r] = lines[r].slice(0, c) + lines[r].slice(c + 1);
					return rebuild(lines, r, c);
				}
				if (r < lines.length - 1) {
					var merged2 = lines[r] + lines[r + 1];
					var newLines3 = lines.slice(0, r).concat([merged2]).concat(lines.slice(r + 2));
					return rebuild(newLines3, r, lines[r].length);
				}
				return s;
			}
			if (msg.runes && msg.runes.length > 0 && key.length === 1) {
				lines[r] = lines[r].slice(0, c) + msg.runes + lines[r].slice(c);
				return rebuild(lines, r, c + msg.runes.length);
			}
			return null;
		},
	};

	// formatDuration handles the small format DSL shared by Stopwatch
	// and Timer: HH/H = hours, MM/M = minutes, SS/S = seconds, SS the
	// hundredth-place fractional, ms = full milliseconds. Anything
	// outside the DSL passes through verbatim so users can sprinkle
	// punctuation freely. Negative inputs floor at 0 — Timer reaches 0
	// then stays.
	function formatDuration(ms, fmt) {
		if (ms < 0) ms = 0;
		var totalMs = Math.floor(ms);
		var totalSec = Math.floor(totalMs / 1000);
		var h = Math.floor(totalSec / 3600);
		var m = Math.floor((totalSec % 3600) / 60);
		var s = totalSec % 60;
		var hundredths = Math.floor((totalMs % 1000) / 10);
		var pad2 = function(n) { return n < 10 ? '0' + n : '' + n; };
		// Tokens are case-insensitive for H/M/S so users can write
		// either Charm/lipgloss-style 'HH:MM:SS' or Go time-style
		// 'hh:mm:ss'. The fractional 'SS' that follows '.' is
		// detected by the preceding char to disambiguate.
		var u = fmt.toUpperCase();
		var out = '';
		var i = 0;
		while (i < fmt.length) {
			if (u.substr(i, 3) === 'HHH') { out += pad2(h); i += 3; continue; }
			if (u.substr(i, 2) === 'HH') { out += pad2(h); i += 2; continue; }
			if (u[i] === 'H') { out += h; i += 1; continue; }
			if (u.substr(i, 2) === 'MS') { out += pad2(hundredths); i += 2; continue; }
			if (u.substr(i, 2) === 'MM') { out += pad2(m); i += 2; continue; }
			if (u[i] === 'M') { out += m; i += 1; continue; }
			if (u.substr(i, 2) === 'SS') {
				var prev = i > 0 ? fmt[i - 1] : '';
				if (prev === '.') out += pad2(hundredths);
				else out += pad2(s);
				i += 2;
				continue;
			}
			if (u[i] === 'S') { out += s; i += 1; continue; }
			out += fmt[i];
			i += 1;
		}
		return out;
	}

	globalThis.Ramune.tui.Stopwatch = function(props) {
		var ms = +props.elapsedMs || 0;
		var fmt = props.format || 'mm:ss.SS';
		var rendered = formatDuration(ms, fmt);
		return styleRest(rendered, props, ['elapsedMs', 'format']);
	};

	globalThis.Ramune.tui.Timer = function(props) {
		var ms = +props.remainingMs || 0;
		var fmt = props.format || 'mm:ss';
		var rendered = formatDuration(ms, fmt);
		// Below the warningAt threshold, swap in warningStyle so users
		// get a "running out" hint without writing the conditional
		// themselves.
		var style = props;
		if (props.warningAt != null && ms <= +props.warningAt) {
			style = Object.assign({}, props, props.warningStyle || { fg: '9', bold: true });
		}
		return styleRest(rendered, style, [
			'remainingMs', 'format', 'warningAt', 'warningStyle',
		]);
	};

	// Stopwatch reducer: counts up. Wall-clock based so missed ticks
	// (the tab was busy with a long update) don't drift the elapsed
	// counter. start/pause toggle running; reset zeroes elapsed +
	// resets the anchor.
	globalThis.Ramune.tui.stopwatch = {
		init: function(opts) {
			opts = opts || {};
			return {
				running: !!opts.running,
				elapsedMs: +opts.elapsedMs || 0,
				anchorAt: opts.running ? Date.now() : 0,
			};
		},
		tick: function(state) {
			if (!state.running) return state;
			var now = Date.now();
			var added = state.anchorAt > 0 ? now - state.anchorAt : 0;
			return { running: true, elapsedMs: state.elapsedMs + added, anchorAt: now };
		},
		toggle: function(state) {
			if (state.running) {
				return { running: false, elapsedMs: state.elapsedMs + (Date.now() - state.anchorAt), anchorAt: 0 };
			}
			return { running: true, elapsedMs: state.elapsedMs, anchorAt: Date.now() };
		},
		reset: function() {
			return { running: false, elapsedMs: 0, anchorAt: 0 };
		},
	};

	// Timer reducer: counts down. Same wall-clock anchoring as
	// Stopwatch. expired flips true the first tick remaining hits 0,
	// so callers can dispatch a "done" msg.
	globalThis.Ramune.tui.timer = {
		init: function(durationMs, opts) {
			opts = opts || {};
			return {
				durationMs: +durationMs || 0,
				remainingMs: +durationMs || 0,
				running: !!opts.running,
				expired: false,
				anchorAt: opts.running ? Date.now() : 0,
			};
		},
		tick: function(state) {
			if (!state.running || state.expired) return state;
			var now = Date.now();
			var elapsed = state.anchorAt > 0 ? now - state.anchorAt : 0;
			var remaining = state.remainingMs - elapsed;
			if (remaining <= 0) {
				return Object.assign({}, state, { running: false, expired: true, remainingMs: 0, anchorAt: 0 });
			}
			return Object.assign({}, state, { remainingMs: remaining, anchorAt: now });
		},
		toggle: function(state) {
			if (state.expired) return state;
			if (state.running) {
				var elapsed = Date.now() - state.anchorAt;
				return Object.assign({}, state, { running: false, remainingMs: state.remainingMs - elapsed, anchorAt: 0 });
			}
			return Object.assign({}, state, { running: true, anchorAt: Date.now() });
		},
		reset: function(state) {
			return Object.assign({}, state, { remainingMs: state.durationMs, running: false, expired: false, anchorAt: 0 });
		},
	};

	// Paginator: page indicator. type='arabic' renders 'X / Y'; type='dots'
	// renders '● ○ ○' style. The component is stateless; the reducer
	// helpers below own the page-state shape so users can advance via
	// keys without rolling their own arithmetic.
	globalThis.Ramune.tui.Paginator = function(props) {
		var page = (props.page | 0) || 0;
		var total = Math.max(1, (props.totalPages | 0) || 1);
		page = Math.max(0, Math.min(page, total - 1));
		var type = props.type || 'arabic';
		var rendered;
		if (type === 'dots') {
			var active = props.activeDot || '●';
			var inactive = props.inactiveDot || '○';
			var dots = [];
			for (var i = 0; i < total; i++) {
				dots.push(i === page ? active : inactive);
			}
			rendered = dots.join(' ');
		} else {
			rendered = (page + 1) + ' / ' + total;
		}
		return styleRest(rendered, props, [
			'page', 'totalPages', 'type', 'activeDot', 'inactiveDot',
		]);
	};
	globalThis.Ramune.tui.paginator = {
		init: function(perPage, totalItems) {
			var pp = Math.max(1, (perPage | 0) || 10);
			var ti = Math.max(0, (totalItems | 0) || 0);
			return {
				page: 0,
				perPage: pp,
				totalItems: ti,
				totalPages: Math.max(1, Math.ceil(ti / pp)),
			};
		},
		setTotal: function(state, totalItems) {
			var ti = Math.max(0, (totalItems | 0) || 0);
			var totalPages = Math.max(1, Math.ceil(ti / state.perPage));
			return Object.assign({}, state, {
				totalItems: ti,
				totalPages: totalPages,
				page: Math.min(state.page, totalPages - 1),
			});
		},
		handleKey: function(state, msg) {
			if (!msg || msg.type !== 'key') return null;
			var key = msg.key;
			if (key === 'left' || key === 'h' || key === 'pgup') {
				return Object.assign({}, state, { page: Math.max(0, state.page - 1) });
			}
			if (key === 'right' || key === 'l' || key === 'pgdown') {
				return Object.assign({}, state, { page: Math.min(state.totalPages - 1, state.page + 1) });
			}
			if (key === 'home') return Object.assign({}, state, { page: 0 });
			if (key === 'end') return Object.assign({}, state, { page: state.totalPages - 1 });
			return null;
		},
		sliceForPage: function(items, state) {
			var start = state.page * state.perPage;
			return items.slice(start, start + state.perPage);
		},
	};

	// Filepicker: stateless renderer of a directory listing. The reducer
	// helpers below own state + Node fs reads; this is just the visual
	// surface so users can theme it (selected style, hidden toggle,
	// max rows) without wiring a full bubbles/filepicker model.
	function formatFileSize(bytes) {
		if (!bytes) return '';
		var u = ['B', 'K', 'M', 'G', 'T'];
		var i = 0;
		var n = bytes;
		while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
		return (i === 0 ? n : n.toFixed(1)) + u[i];
	}
	globalThis.Ramune.tui.Filepicker = function(props) {
		var entries = props.entries || [];
		var sel = Math.max(0, Math.min((props.selected | 0) || 0, entries.length - 1));
		var showHidden = !!props.showHidden;
		var maxRows = (props.maxRows | 0) || 12;
		var dirIcon = props.dirIcon || '▸';
		var fileIcon = props.fileIcon || ' ';
		var visible = entries;
		if (!showHidden) {
			visible = entries.filter(function(e) { return e.name === '..' || e.name[0] !== '.'; });
		}
		var half = Math.floor(maxRows / 2);
		var start = Math.max(0, Math.min(visible.length - maxRows, sel - half));
		var end = Math.min(visible.length, start + maxRows);
		var rows = [];
		for (var i = start; i < end; i++) {
			var e = visible[i];
			var icon = e.isDir ? dirIcon : fileIcon;
			var name = e.name + (e.isDir ? '/' : '');
			var size = e.isDir ? '' : formatFileSize(e.size);
			var line = (i === sel ? '› ' : '  ') + icon + ' ' + name;
			if (size) line += '  ' + globalThis.Ramune.tui.style(size, { fg: '242' });
			if (i === sel && props.selectedStyle) line = globalThis.Ramune.tui.style(line, props.selectedStyle);
			rows.push(line);
		}
		if (start > 0) rows.unshift(globalThis.Ramune.tui.style('  …', { fg: '242' }));
		if (end < visible.length) rows.push(globalThis.Ramune.tui.style('  …', { fg: '242' }));
		var crumb = props.cwd
			? globalThis.Ramune.tui.style(props.cwd, props.cwdStyle || { fg: '12', bold: true }) + '\n'
			: '';
		return styleRest(crumb + rows.join('\n'), props, [
			'entries', 'selected', 'showHidden', 'maxRows',
			'cwd', 'cwdStyle', 'dirIcon', 'fileIcon', 'selectedStyle',
		]);
	};
	// Reducer + fs helpers. Synchronous reads are fine for picker
	// startup and cd — the listings are typically small. For huge
	// directories the user can swap in fs.promises.readdir + their
	// own dispatch wiring.
	globalThis.Ramune.tui.filepicker = {
		readDirSync: function(cwd) {
			var fs = require('fs');
			var path = require('path');
			var names;
			try { names = fs.readdirSync(cwd); }
			catch (e) { return []; }
			names.sort();
			var out = [{ name: '..', isDir: true }];
			for (var i = 0; i < names.length; i++) {
				try {
					var st = fs.statSync(path.join(cwd, names[i]));
					out.push({ name: names[i], isDir: st.isDirectory(), size: st.size || 0 });
				} catch (e) { /* skip unreadable */ }
			}
			return out;
		},
		init: function(cwd) {
			var path = require('path');
			var resolved = cwd ? path.resolve(cwd) : process.cwd();
			return {
				cwd: resolved,
				entries: globalThis.Ramune.tui.filepicker.readDirSync(resolved),
				selected: 0,
				showHidden: false,
			};
		},
		cd: function(state, target) {
			var path = require('path');
			var nextCwd = target === '..'
				? path.dirname(state.cwd)
				: path.join(state.cwd, target);
			return Object.assign({}, state, {
				cwd: nextCwd,
				entries: globalThis.Ramune.tui.filepicker.readDirSync(nextCwd),
				selected: 0,
			});
		},
		handleKey: function(state, msg) {
			if (!msg || msg.type !== 'key') return null;
			var s = state;
			var visible = s.showHidden
				? s.entries
				: s.entries.filter(function(e) { return e.name === '..' || e.name[0] !== '.'; });
			var sel = Math.min(s.selected, visible.length - 1);
			if (msg.key === 'up' || msg.key === 'k') {
				return Object.assign({}, s, { selected: Math.max(0, sel - 1) });
			}
			if (msg.key === 'down' || msg.key === 'j') {
				return Object.assign({}, s, { selected: Math.min(visible.length - 1, sel + 1) });
			}
			if (msg.key === 'home') return Object.assign({}, s, { selected: 0 });
			if (msg.key === 'end') return Object.assign({}, s, { selected: visible.length - 1 });
			if (msg.key === '.') return Object.assign({}, s, { showHidden: !s.showHidden, selected: 0 });
			if (msg.key === 'left' || msg.key === 'h' || msg.key === 'backspace') {
				return globalThis.Ramune.tui.filepicker.cd(s, '..');
			}
			if (msg.key === 'enter' || msg.key === 'right' || msg.key === 'l') {
				var entry = visible[sel];
				if (!entry) return s;
				if (entry.name === '..') return globalThis.Ramune.tui.filepicker.cd(s, '..');
				if (entry.isDir) return globalThis.Ramune.tui.filepicker.cd(s, entry.name);
				// File: surface the selection for the caller via an
				// _opened tag instead of mutating state. Caller can
				// pluck path off the returned object and decide.
				var path = require('path');
				return Object.assign({}, s, { _opened: path.join(s.cwd, entry.name) });
			}
			return null;
		},
	};

	// Table: header row + body with selection cursor, optional sort
	// indicator on the active column. columns is [{title, width,
	// align?}]; rows is string[][]. The component is stateless — sort
	// the rows yourself before passing them in. headerStyle /
	// rowStyle / selectedStyle theme the three layers.
	function alignCell(text, width, align) {
		var s = String(text);
		if (s.length > width) return s.slice(0, Math.max(0, width - 1)) + '…';
		var pad = width - s.length;
		if (align === 'right') return ' '.repeat(pad) + s;
		if (align === 'center') {
			var l = Math.floor(pad / 2);
			return ' '.repeat(l) + s + ' '.repeat(pad - l);
		}
		return s + ' '.repeat(pad);
	}
	globalThis.Ramune.tui.Table = function(props) {
		var cols = props.columns || [];
		var rows = props.rows || [];
		var sel = Math.max(0, Math.min((props.selected | 0) || 0, rows.length - 1));
		var maxRows = (props.maxRows | 0) || rows.length;
		var sortColumn = props.sortColumn != null ? props.sortColumn | 0 : -1;
		var sortDir = props.sortDir || '';
		var headerStyle = props.headerStyle || { bold: true, fg: '12' };
		var selectedStyle = props.selectedStyle || { bold: true, bg: '237' };
		var rowSep = props.cellSep || ' │ ';
		// Header row. Pad to a width that fits the sort indicator so
		// truncation doesn't eat the arrow.
		var header = cols.map(function(c, i) {
			var label = c.title || '';
			if (i === sortColumn) {
				label += sortDir === 'desc' ? ' ↓' : ' ↑';
			}
			var w = Math.max(c.width || 0, label.length);
			return alignCell(label, w, c.align || 'left');
		}).join(rowSep);
		var divider = cols.map(function(c) {
			return '─'.repeat(c.width || 0);
		}).join('─┼─');
		// Body rows: viewport around the selection.
		var half = Math.floor(maxRows / 2);
		var start = Math.max(0, Math.min(rows.length - maxRows, sel - half));
		var end = Math.min(rows.length, start + maxRows);
		var body = [];
		for (var r = start; r < end; r++) {
			var cells = cols.map(function(c, i) {
				return alignCell(rows[r][i] || '', c.width || 0, c.align || 'left');
			}).join(rowSep);
			body.push(r === sel ? globalThis.Ramune.tui.style(cells, selectedStyle) : cells);
		}
		var lines = [
			globalThis.Ramune.tui.style(header, headerStyle),
			globalThis.Ramune.tui.style(divider, { fg: '238' }),
		].concat(body);
		// Pagination indicators outside the styled body so they don't
		// inherit the selection background.
		if (start > 0) lines.splice(2, 0, globalThis.Ramune.tui.style('  ↑ ' + start + ' more', { fg: '242' }));
		if (end < rows.length) lines.push(globalThis.Ramune.tui.style('  ↓ ' + (rows.length - end) + ' more', { fg: '242' }));
		return styleRest(lines.join('\n'), props, [
			'columns', 'rows', 'selected', 'maxRows', 'sortColumn',
			'sortDir', 'headerStyle', 'rowStyle', 'selectedStyle', 'cellSep',
		]);
	};

	// Form: huh-style multi-field composer. Field types accepted today:
	//   text       single-line input (delegates to Ramune.tui.input)
	//   textarea   multi-line input (delegates to Ramune.tui.textarea)
	//   select     pick one of options
	//   multi      pick zero or more of options
	//   confirm    yes/no boolean
	// State shape:
	//   { fields: Field[], focused: number, submitted: boolean,
	//     errors: { [name]: string } }
	// Each field carries its own value + cursor; the helpers below own
	// the routing so update() stays terse.
	function newFieldState(field) {
		var f = Object.assign({}, field);
		switch (f.type) {
			case 'text':
				if (f.value == null) f.value = '';
				if (f.cursor == null) f.cursor = String(f.value).length;
				break;
			case 'textarea':
				if (f.value == null) f.value = '';
				if (f.cursor == null) {
					var lines = String(f.value).split('\n');
					f.cursor = { row: lines.length - 1, col: lines[lines.length - 1].length };
				}
				break;
			case 'select':
				if (f.value == null && f.options && f.options.length > 0) f.value = f.options[0];
				break;
			case 'multi':
				if (!Array.isArray(f.value)) f.value = [];
				break;
			case 'confirm':
				if (f.value == null) f.value = false;
				break;
		}
		return f;
	}
	globalThis.Ramune.tui.form = {
		init: function(fields) {
			return {
				fields: (fields || []).map(newFieldState),
				focused: 0,
				submitted: false,
				errors: {},
			};
		},
		handleKey: function(state, msg) {
			if (!msg || msg.type !== 'key' || state.submitted) return null;
			var fields = state.fields.slice();
			var idx = state.focused;
			var f = fields[idx];
			if (!f) return null;
			var key = msg.key;
			// Field-internal first, then form-wide.
			if (f.type === 'text' && key !== 'tab' && key !== 'enter' && key !== 'shift+tab') {
				var ns = globalThis.Ramune.tui.input.handleKey({ value: f.value, cursor: f.cursor }, msg);
				if (ns) {
					fields[idx] = Object.assign({}, f, ns);
					return Object.assign({}, state, { fields: fields });
				}
			}
			if (f.type === 'textarea' && key !== 'tab' && key !== 'shift+tab' && !(key === 'enter' && msg.alt === false && msg.runes === '')) {
				var ns2 = globalThis.Ramune.tui.textarea.handleKey({ value: f.value, cursor: f.cursor }, msg);
				if (ns2) {
					fields[idx] = Object.assign({}, f, ns2);
					return Object.assign({}, state, { fields: fields });
				}
			}
			if (f.type === 'select') {
				if (key === 'up' || key === 'left' || key === 'k' || key === 'h') {
					var i = Math.max(0, f.options.indexOf(f.value) - 1);
					fields[idx] = Object.assign({}, f, { value: f.options[i] });
					return Object.assign({}, state, { fields: fields });
				}
				if (key === 'down' || key === 'right' || key === 'j' || key === 'l') {
					var i2 = Math.min(f.options.length - 1, f.options.indexOf(f.value) + 1);
					fields[idx] = Object.assign({}, f, { value: f.options[i2] });
					return Object.assign({}, state, { fields: fields });
				}
			}
			if (f.type === 'multi') {
				if (key === 'up' || key === 'k') {
					fields[idx] = Object.assign({}, f, { _cursor: Math.max(0, (f._cursor || 0) - 1) });
					return Object.assign({}, state, { fields: fields });
				}
				if (key === 'down' || key === 'j') {
					fields[idx] = Object.assign({}, f, { _cursor: Math.min(f.options.length - 1, (f._cursor || 0) + 1) });
					return Object.assign({}, state, { fields: fields });
				}
				if (key === ' ' || key === 'x') {
					var c = f._cursor || 0;
					var opt = f.options[c];
					var v = (f.value || []).slice();
					var pos = v.indexOf(opt);
					if (pos >= 0) v.splice(pos, 1); else v.push(opt);
					fields[idx] = Object.assign({}, f, { value: v });
					return Object.assign({}, state, { fields: fields });
				}
			}
			if (f.type === 'confirm') {
				// 'tab' falls through to the form-wide nav block below.
				if (key === 'left' || key === 'right' || key === 'h' || key === 'l' || key === ' ') {
					fields[idx] = Object.assign({}, f, { value: !f.value });
					return Object.assign({}, state, { fields: fields });
				}
				if (key === 'y') { fields[idx] = Object.assign({}, f, { value: true }); return Object.assign({}, state, { fields: fields }); }
				if (key === 'n') { fields[idx] = Object.assign({}, f, { value: false }); return Object.assign({}, state, { fields: fields }); }
			}
			// Form-wide nav.
			if (key === 'tab' || (f.type !== 'textarea' && key === 'enter' && idx < fields.length - 1)) {
				return Object.assign({}, state, { focused: Math.min(fields.length - 1, idx + 1) });
			}
			if (key === 'shift+tab') {
				return Object.assign({}, state, { focused: Math.max(0, idx - 1) });
			}
			if (key === 'enter' && idx === fields.length - 1) {
				// Validate everything; submit only when clean.
				var errors = {};
				for (var i3 = 0; i3 < fields.length; i3++) {
					var ff = fields[i3];
					if (ff.required && (ff.value == null || ff.value === '')) {
						errors[ff.name] = 'required';
					} else if (typeof ff.validate === 'function') {
						var err = ff.validate(ff.value);
						if (err) errors[ff.name] = err;
					}
				}
				var clean = Object.keys(errors).length === 0;
				return Object.assign({}, state, {
					errors: errors,
					submitted: clean,
					focused: clean ? idx : fields.findIndex(function(ff) { return errors[ff.name]; }),
				});
			}
			return null;
		},
		// Convenience extractor for downstream callers — flatten field
		// state to {name: value} ignoring cursors and meta.
		values: function(state) {
			var out = {};
			(state.fields || []).forEach(function(f) { out[f.name] = f.value; });
			return out;
		},
	};
	globalThis.Ramune.tui.Form = function(props) {
		var state = props.state;
		if (!state) return '';
		var lines = [];
		for (var i = 0; i < state.fields.length; i++) {
			var f = state.fields[i];
			var focused = i === state.focused && !state.submitted;
			var label = focused
				? globalThis.Ramune.tui.style('› ' + (f.label || f.name), { bold: true, fg: '12' })
				: globalThis.Ramune.tui.style('  ' + (f.label || f.name), { fg: '252' });
			var fieldRender;
			switch (f.type) {
				case 'text':
					fieldRender = globalThis.Ramune.tui.Input({
						value: f.value,
						cursor: f.cursor,
						placeholder: f.placeholder,
						focused: focused,
						width: f.width || 40,
					});
					break;
				case 'textarea':
					fieldRender = globalThis.Ramune.tui.Textarea({
						value: f.value,
						cursor: f.cursor,
						focused: focused,
						rows: f.rows || 3,
						cols: f.cols || 50,
						border: 'rounded',
						padding: [0, 1],
					});
					break;
				case 'select':
					fieldRender = (f.options || []).map(function(opt) {
						var marker = opt === f.value ? '◉' : '○';
						var s = marker + ' ' + opt;
						return opt === f.value && focused
							? globalThis.Ramune.tui.style(s, { bold: true, fg: '12' })
							: globalThis.Ramune.tui.style(s, { fg: '252' });
					}).join('   ');
					break;
				case 'multi':
					fieldRender = (f.options || []).map(function(opt, ii) {
						var checked = (f.value || []).indexOf(opt) >= 0;
						var marker = checked ? '☑' : '☐';
						var cursor = focused && ii === (f._cursor || 0) ? '› ' : '  ';
						var s = cursor + marker + ' ' + opt;
						return ii === (f._cursor || 0) && focused
							? globalThis.Ramune.tui.style(s, { bold: true, fg: '12' })
							: globalThis.Ramune.tui.style(s, { fg: '252' });
					}).join('\n');
					break;
				case 'confirm':
					var yes = f.value
						? globalThis.Ramune.tui.style('● Yes', { bold: true, fg: '10' })
						: globalThis.Ramune.tui.style('○ Yes', { fg: '252' });
					var no = !f.value
						? globalThis.Ramune.tui.style('● No', { bold: true, fg: '9' })
						: globalThis.Ramune.tui.style('○ No', { fg: '252' });
					fieldRender = yes + '   ' + no;
					break;
				default:
					fieldRender = globalThis.Ramune.tui.style('[unknown field type ' + f.type + ']', { fg: '9' });
			}
			lines.push(label);
			lines.push('  ' + fieldRender.replace(/\n/g, '\n  '));
			if (state.errors[f.name]) {
				lines.push(globalThis.Ramune.tui.style('  ! ' + state.errors[f.name], { fg: '9' }));
			}
			lines.push('');
		}
		if (state.submitted) {
			lines.push(globalThis.Ramune.tui.style('✓ submitted', { bold: true, fg: '10' }));
		}
		return styleRest(lines.join('\n'), props, ['state']);
	};

	// keymap(bindings, opts?) bundles a set of bindings with help labels.
	// Each binding's keys entry can be a single key ('q', 'ctrl+c') or a
	// space-separated chord sequence ('g g' = press g then g). The
	// returned object exposes:
	//   match(msg)              matched name, or null. Stateful;
	//                           tracks recent keys for sequences with
	//                           an opts.staleMs timeout (default 800).
	//   matches(msg, name)      bool, single-press only. Preserved for
	//                           callers driving their own routing
	//                           without the sequence buffer.
	//   helpEntries(names?)     [{ key, desc }, ...]
	//   bindingFor(name)        the underlying entry.
	//   setDisabled(name, bool) toggle without rebuilding.
	//   reset()                 clear the pending sequence buffer.
	globalThis.Ramune.tui.keymap = function(bindings, opts) {
		bindings = bindings || {};
		opts = opts || {};
		var staleMs = +opts.staleMs || 800;
		var names = Object.keys(bindings);
		// Pre-parse keys into Array<Array<string>>: outer is the
		// alternative list, inner is the chord sequence.
		for (var i = 0; i < names.length; i++) {
			var b = bindings[names[i]];
			b._sequences = (b.keys || []).map(function(k) {
				return k.indexOf(' ') >= 0 ? k.split(/\s+/) : [k];
			});
		}
		var pending = [];
		var lastAt = 0;
		function arrayEq(a, b) {
			if (a.length !== b.length) return false;
			for (var i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
			return true;
		}
		function findExactOrPrefix(buf) {
			var hasPrefix = false;
			for (var i = 0; i < names.length; i++) {
				var b = bindings[names[i]];
				if (b.disabled) continue;
				for (var s = 0; s < b._sequences.length; s++) {
					var seq = b._sequences[s];
					if (arrayEq(seq, buf)) return { name: names[i], hasPrefix: false };
					if (seq.length > buf.length && arrayEq(seq.slice(0, buf.length), buf)) {
						hasPrefix = true;
					}
				}
			}
			return { name: null, hasPrefix: hasPrefix };
		}
		function match(msg) {
			if (!msg || msg.type !== 'key') return null;
			var now = Date.now();
			if (now - lastAt > staleMs) pending = [];
			lastAt = now;
			pending.push(msg.key);
			// Try the full pending buffer first; if it doesn't hit but
			// is a prefix of some binding, hold it. Otherwise drop the
			// oldest key and retry — handles the case where a stale
			// 'g' from an aborted sequence still has a fresh 'q'
			// binding to fire.
			while (pending.length > 0) {
				var r = findExactOrPrefix(pending);
				if (r.name) {
					pending = [];
					return r.name;
				}
				if (r.hasPrefix) return null;
				pending.shift();
			}
			return null;
		}
		function matches(msg, name) {
			if (!msg || msg.type !== 'key') return false;
			var b = bindings[name];
			if (!b || b.disabled) return false;
			for (var s = 0; s < b._sequences.length; s++) {
				var seq = b._sequences[s];
				if (seq.length === 1 && seq[0] === msg.key) return true;
			}
			return false;
		}
		function helpEntries(picked) {
			var src = picked && picked.length ? picked : names;
			var out = [];
			for (var i = 0; i < src.length; i++) {
				var b = bindings[src[i]];
				if (!b || b.disabled || !b.help) continue;
				out.push({ key: b.help.key, desc: b.help.desc });
			}
			return out;
		}
		return {
			match: match,
			matches: matches,
			helpEntries: helpEntries,
			bindingFor: function(name) { return bindings[name]; },
			setDisabled: function(name, disabled) {
				if (bindings[name]) bindings[name].disabled = !!disabled;
			},
			reset: function() { pending = []; lastAt = 0; },
		};
	};

	// Reducer-style helpers for the Input state shape. Userland calls
	// these from update() so the keystroke handling stays one-liner.
	// State shape: { value: string, cursor: number }
	globalThis.Ramune.tui.input = {
		init: function(value) {
			var v = value == null ? '' : String(value);
			return { value: v, cursor: v.length };
		},
		// handleKey returns the new state when the key is a printable
		// character or one of the navigation/edit keys; null otherwise
		// so callers can fall through to their own routing.
		handleKey: function(state, msg) {
			if (!msg || msg.type !== 'key') return null;
			var s = state || { value: '', cursor: 0 };
			var key = msg.key;
			if (key === 'left') return { value: s.value, cursor: Math.max(0, s.cursor - 1) };
			if (key === 'right') return { value: s.value, cursor: Math.min(s.value.length, s.cursor + 1) };
			if (key === 'home') return { value: s.value, cursor: 0 };
			if (key === 'end') return { value: s.value, cursor: s.value.length };
			if (key === 'backspace') {
				if (s.cursor === 0) return s;
				return {
					value: s.value.slice(0, s.cursor - 1) + s.value.slice(s.cursor),
					cursor: s.cursor - 1,
				};
			}
			if (key === 'delete') {
				if (s.cursor === s.value.length) return s;
				return {
					value: s.value.slice(0, s.cursor) + s.value.slice(s.cursor + 1),
					cursor: s.cursor,
				};
			}
			// Plain printable input arrives as msg.runes (single rune
			// in most cases). Skip non-printable keys.
			if (msg.runes && msg.runes.length > 0 && key.length === 1) {
				return {
					value: s.value.slice(0, s.cursor) + msg.runes + s.value.slice(s.cursor),
					cursor: s.cursor + msg.runes.length,
				};
			}
			return null;
		},
	};
})();`)
}

func goTUIStart(rt *Runtime, mgr *tuiManager) func([]any) (any, error) {
	return func(args []any) (any, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("tui.run: need (initialStateJSON, updateFn, viewFn, doneFn[, opts])")
		}
		initialJSON, _ := args[0].(string)
		updateFn, ok := args[1].(*JSFunc)
		if !ok {
			return nil, fmt.Errorf("tui.run: update must be a function")
		}
		viewFn, ok := args[2].(*JSFunc)
		if !ok {
			return nil, fmt.Errorf("tui.run: view must be a function")
		}
		doneFn, ok := args[3].(*JSFunc)
		if !ok {
			return nil, fmt.Errorf("tui.run: done must be a function")
		}
		fullscreen := false
		mouseMotion := false
		headless := false
		if len(args) >= 5 {
			if opts, ok := args[4].(map[string]any); ok {
				if v, ok := opts["fullscreen"].(bool); ok {
					fullscreen = v
				}
				if v, ok := opts["mouse"].(bool); ok {
					mouseMotion = v
				}
				if v, ok := opts["headless"].(bool); ok {
					headless = v
				}
			}
		}
		mgr.mu.Lock()
		mgr.next++
		id := mgr.next
		sess := &tuiSession{rt: rt, id: id, update: updateFn, view: viewFn, done: doneFn}
		sess.stateRaw.Store(initialJSON)
		mgr.sessions[id] = sess
		mgr.mu.Unlock()

		go func() {
			progOpts := []tea.ProgramOption{}
			if fullscreen {
				progOpts = append(progOpts, tea.WithAltScreen())
			}
			if mouseMotion {
				progOpts = append(progOpts, tea.WithMouseAllMotion())
			}
			if headless {
				// Pipe reader as stdin so tea doesn't try to open
				// /dev/tty, plus a bytes.Buffer for captured output.
				// closeInput unblocks the reader at shutdown so the
				// tea goroutine doesn't hang on its input loop.
				// tea.WithoutSignalHandler keeps tea from catching
				// SIGINT/SIGTERM — JSC's signal-sensitive cgo path
				// crashes when tea's handler delivers signals during
				// a JSC eval.
				pr, pw := io.Pipe()
				sess.closeInput = func() { _ = pw.Close() }
				sess.outBuf = &bytes.Buffer{}
				progOpts = append(progOpts,
					tea.WithInput(pr),
					tea.WithOutput(sess.outBuf),
					tea.WithoutSignalHandler(),
					tea.WithoutCatchPanics(),
				)
			}
			prog := tea.NewProgram(tuiModel{sess: sess}, progOpts...)
			sess.prog = prog
			_, runErr := prog.Run()
			if sess.closeInput != nil {
				sess.closeInput()
			}
			final, _ := sess.stateRaw.Load().(string)
			var captured string
			if sess.outBuf != nil {
				captured = sess.outBuf.String()
			}
			ev := tuiDoneEvent{done: sess.done, sess: sess, finalState: final, captured: captured}
			if runErr != nil {
				ev.err = runErr.Error()
			}
			// Hand the completion off to the JSC tick — calling doneFn
			// from this worker would race Runtime.Close shutting down
			// libJavaScriptCore (intermittent SEGV inside JSC).
			mgr.mu.Lock()
			mgr.pending = append(mgr.pending, ev)
			mgr.mu.Unlock()
			rt.Wake()
		}()

		return float64(id), nil
	}
}

// tuiDispatchMsg is the wrapper Send'd back into BubbleTea so Update
// sees an opaque tea.Msg the model layer can route to JS update().
type tuiDispatchMsg struct {
	payload any
}

func goTUIDispatch(mgr *tuiManager) func([]any) (any, error) {
	return func(args []any) (any, error) {
		// Two shapes: dispatch(msg) → latest session, or
		// dispatch(sessionId, msg). Multi-session use cases are rare,
		// but supported so a host with several concurrent TUIs can
		// route precisely.
		var (
			id  uint64
			msg any
		)
		switch {
		case len(args) >= 2:
			if f, ok := args[0].(float64); ok {
				id = uint64(f)
			}
			msg = args[1]
		case len(args) == 1:
			msg = args[0]
		default:
			return nil, fmt.Errorf("tui.dispatch: msg required")
		}
		mgr.mu.Lock()
		var sess *tuiSession
		if id != 0 {
			sess = mgr.sessions[id]
		} else {
			// Latest active session — the "default" most apps want.
			var newest uint64
			for k, s := range mgr.sessions {
				s.mu.Lock()
				live := !s.exited
				s.mu.Unlock()
				if live && k > newest {
					newest = k
					sess = s
				}
			}
		}
		mgr.mu.Unlock()
		if sess == nil || sess.prog == nil {
			return nil, nil // no active program — silently drop
		}
		sess.prog.Send(tuiDispatchMsg{payload: msg})
		return nil, nil
	}
}

func goTUIQuit(mgr *tuiManager) func([]any) (any, error) {
	return func(args []any) (any, error) {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		if len(args) >= 1 {
			if id, ok := args[0].(float64); ok && id > 0 {
				if s, ok := mgr.sessions[uint64(id)]; ok && s.prog != nil {
					s.prog.Quit()
				}
				return nil, nil
			}
		}
		// No id → quit all (used by Ramune.tui.quit() without args).
		for _, s := range mgr.sessions {
			if s.prog != nil {
				s.prog.Quit()
			}
		}
		return nil, nil
	}
}

// markdownRendererCache memoizes glamour TermRenderers by (theme, width)
// so per-frame view() rendering doesn't rebuild the chroma syntax
// highlighter + theme JSON parser on every key press. Without this the
// theme switcher demo eventually freezes once GC churn from repeated
// renderer construction outpaces the JSC dispatch loop.
var (
	markdownRendererMu    sync.Mutex
	markdownRendererCache = map[string]*glamour.TermRenderer{}
)

// goTUIMarkdown lowers a markdown string to an ANSI-styled terminal
// rendering via glamour. opts: { theme: 'dark'|'light'|'notty'|'ascii'|
// 'auto'|'pink'|'dracula'|'tokyo-night', width: int }. Defaults to
// 'auto' (auto-detect bg) and 80 cols.
func goTUIMarkdown(args []any) (any, error) {
	if len(args) < 1 {
		return "", nil
	}
	text, _ := args[0].(string)
	theme := "auto"
	width := 80
	if len(args) >= 2 {
		if opts, ok := args[1].(map[string]any); ok {
			if v, ok := opts["theme"].(string); ok && v != "" {
				theme = v
			}
			if v, ok := opts["width"].(float64); ok && v > 0 {
				width = int(v)
			}
		}
	}
	r, err := getOrCreateMarkdownRenderer(theme, width)
	if err != nil {
		return nil, err
	}
	out, err := r.Render(text)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func getOrCreateMarkdownRenderer(theme string, width int) (*glamour.TermRenderer, error) {
	key := theme + "|" + fmt.Sprintf("%d", width)
	markdownRendererMu.Lock()
	defer markdownRendererMu.Unlock()
	if r, ok := markdownRendererCache[key]; ok {
		return r, nil
	}
	var styleOpt glamour.TermRendererOption
	switch theme {
	case "auto":
		styleOpt = glamour.WithAutoStyle()
	case "dark", "light", "notty", "ascii":
		styleOpt = glamour.WithStandardStyle(theme)
	default:
		// Pass through to glamour's named-style loader so Charm's
		// extras (pink, dracula, tokyo-night, etc.) work without us
		// hard-coding the list.
		styleOpt = glamour.WithStandardStyle(theme)
	}
	r, err := glamour.NewTermRenderer(styleOpt, glamour.WithWordWrap(width))
	if err != nil {
		return nil, err
	}
	markdownRendererCache[key] = r
	return r, nil
}

// goTUIStyle wraps Lipgloss into a one-shot styling helper. Userland
// builds an options object (`{ bold: true, fg: '12', padding: [0,1] }`)
// and gets the rendered string back. Keeps the API surface small while
// covering the common Charm idioms: foreground/background/bold/italic/
// underline/border/padding/margin/width/height/align.
func goTUIStyle(args []any) (any, error) {
	if len(args) < 1 {
		return "", nil
	}
	text, _ := args[0].(string)
	style := lipgloss.NewStyle()
	if len(args) >= 2 {
		if opts, ok := args[1].(map[string]any); ok {
			style = applyStyleOpts(style, opts)
		}
	}
	return style.Render(text), nil
}

func applyStyleOpts(s lipgloss.Style, opts map[string]any) lipgloss.Style {
	if v, ok := opts["bold"].(bool); ok && v {
		s = s.Bold(true)
	}
	if v, ok := opts["italic"].(bool); ok && v {
		s = s.Italic(true)
	}
	if v, ok := opts["underline"].(bool); ok && v {
		s = s.Underline(true)
	}
	if v, ok := opts["faint"].(bool); ok && v {
		s = s.Faint(true)
	}
	if v, ok := opts["fg"].(string); ok && v != "" {
		s = s.Foreground(lipgloss.Color(v))
	}
	if v, ok := opts["bg"].(string); ok && v != "" {
		s = s.Background(lipgloss.Color(v))
	}
	// Padding accepts a single number ([all]) or [vertical, horizontal]
	// or [top, right, bottom, left]. Mirrors Lipgloss's variadic API.
	if v, ok := opts["padding"]; ok {
		if ints := numList(v); len(ints) > 0 {
			s = s.Padding(ints...)
		}
	}
	if v, ok := opts["margin"]; ok {
		if ints := numList(v); len(ints) > 0 {
			s = s.Margin(ints...)
		}
	}
	if v, ok := opts["width"].(float64); ok {
		s = s.Width(int(v))
	}
	if v, ok := opts["height"].(float64); ok {
		s = s.Height(int(v))
	}
	if v, ok := opts["align"].(string); ok {
		switch v {
		case "left":
			s = s.Align(lipgloss.Left)
		case "center":
			s = s.Align(lipgloss.Center)
		case "right":
			s = s.Align(lipgloss.Right)
		}
	}
	if v, ok := opts["border"].(string); ok {
		switch v {
		case "rounded":
			s = s.Border(lipgloss.RoundedBorder())
		case "thick":
			s = s.Border(lipgloss.ThickBorder())
		case "double":
			s = s.Border(lipgloss.DoubleBorder())
		case "normal", "single":
			s = s.Border(lipgloss.NormalBorder())
		case "hidden":
			s = s.Border(lipgloss.HiddenBorder())
		}
	}
	return s
}

func numList(v any) []int {
	switch x := v.(type) {
	case float64:
		return []int{int(x)}
	case []any:
		out := make([]int, 0, len(x))
		for _, e := range x {
			if n, ok := e.(float64); ok {
				out = append(out, int(n))
			}
		}
		return out
	}
	return nil
}
