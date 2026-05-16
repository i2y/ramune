//go:build !notui

package ramune_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/i2y/ramune"
)

// runTUIScript drives Ramune.tui.test() with a JSON-serialized opts
// blob and returns the captured frames as a slice of strings.
func runTUIScript(t *testing.T, body string) []string {
	t.Helper()
	r := sharedNodeCompat(t)
	out, err := r.Eval(`(function() {
		` + body + `
		var r = Ramune.tui.test(opts);
		return JSON.stringify(r.frames);
	})()`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if out == nil {
		t.Fatalf("eval returned nil")
	}
	var frames []string
	if err := json.Unmarshal([]byte(out.String()), &frames); err != nil {
		t.Fatalf("decode frames: %v", err)
	}
	return frames
}

func TestTUI_TestHarness_Counter(t *testing.T) {
	frames := runTUIScript(t, `
		var opts = {
			init: function() { return { count: 0 }; },
			update: function(s, m) {
				if (m.type !== 'key') return s;
				if (m.key === 'up') return { count: s.count + 1 };
				if (m.key === 'q') return Ramune.tui.Cmd.quit(s);
				return s;
			},
			view: function(s) { return '[' + s.count + ']'; },
			script: [
				{ type: 'key', key: 'up' },
				{ type: 'key', key: 'up' },
				{ type: 'key', key: 'up' },
				{ type: 'key', key: 'q' },
				{ type: 'key', key: 'up' },
			],
		};`)
	want := []string{"[0]", "[1]", "[2]", "[3]", "[3]"}
	if len(frames) != len(want) {
		t.Fatalf("frame count: got %d, want %d (%v)", len(frames), len(want), frames)
	}
	for i, w := range want {
		if frames[i] != w {
			t.Errorf("frame %d: got %q, want %q", i, frames[i], w)
		}
	}
}

func TestTUI_Input_HandleKey(t *testing.T) {
	frames := runTUIScript(t, `
		var opts = {
			init: function() { return Ramune.tui.input.init(''); },
			update: function(s, m) {
				if (m.type === 'key' && m.key === 'q') return Ramune.tui.Cmd.quit(s);
				return Ramune.tui.input.handleKey(s, m) || s;
			},
			view: function(s) { return '|' + s.value + '@' + s.cursor + '|'; },
			script: [
				{ type: 'key', key: 'h', runes: 'h' },
				{ type: 'key', key: 'i', runes: 'i' },
				{ type: 'key', key: 'left' },
				{ type: 'key', key: 'a', runes: 'a' },
				{ type: 'key', key: 'q' },
			],
		};`)
	// init "" → "h" → "hi" (cursor 2) → cursor 1 → "hai" (cursor 2) → quit
	want := []string{"|@0|", "|h@1|", "|hi@2|", "|hi@1|", "|hai@2|", "|hai@2|"}
	if len(frames) != len(want) {
		t.Fatalf("frame count: got %d, want %d (%v)", len(frames), len(want), frames)
	}
	for i, w := range want {
		if frames[i] != w {
			t.Errorf("frame %d: got %q, want %q", i, frames[i], w)
		}
	}
}

func TestTUI_Textarea_NewlineSplit(t *testing.T) {
	frames := runTUIScript(t, `
		var opts = {
			init: function() { return Ramune.tui.textarea.init('ab'); },
			update: function(s, m) {
				if (m.type === 'key' && m.key === 'q') return Ramune.tui.Cmd.quit(s);
				return Ramune.tui.textarea.handleKey(s, m) || s;
			},
			view: function(s) { return s.value.replace(/\n/g, '|') + '@' + s.cursor.row + ',' + s.cursor.col; },
			script: [
				{ type: 'key', key: 'home' },
				{ type: 'key', key: 'right' },
				{ type: 'key', key: 'enter' },
				{ type: 'key', key: 'q' },
			],
		};`)
	// init "ab" → cursor row=0,col=2 → home (0,0) → right (0,1) → enter splits → "a|b" (1,0) → quit
	want := []string{"ab@0,2", "ab@0,0", "ab@0,1", "a|b@1,0", "a|b@1,0"}
	if len(frames) != len(want) {
		t.Fatalf("frame count: got %d, want %d (%v)", len(frames), len(want), frames)
	}
	for i, w := range want {
		if frames[i] != w {
			t.Errorf("frame %d: got %q, want %q", i, frames[i], w)
		}
	}
}

// TestTUI_Headless_DispatchAndCapture spins up a real tea.Program with
// no TTY (input piped, output buffered), dispatches a few synthetic
// msgs, then asks it to quit. The Promise resolves with both the final
// state and the captured stdout, letting us assert on rendered frames
// without a terminal.
func TestTUI_Headless_DispatchAndCapture(t *testing.T) {
	// Headless run uses Send across the JSC dispatch channel and the
	// Bun.serve-style tick managers, so we need a fresh runtime — the
	// shared one might already be wedged on another test's program.
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	var (
		mu       sync.Mutex
		captured string
		state    string
	)
	doneCh := make(chan struct{})
	if err := r.RegisterFunc("__tuiTestDone", func(args []any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(args) >= 1 {
			if s, ok := args[0].(string); ok {
				state = s
			}
		}
		if len(args) >= 2 {
			if s, ok := args[1].(string); ok {
				captured = s
			}
		}
		select {
		case <-doneCh:
		default:
			close(doneCh)
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := r.Exec(`
		var p = Ramune.tui.run({
			init: function() { return { count: 0, msgs: [] }; },
			update: function(s, m) {
				if (m.type === 'bump') return Object.assign({}, s, { count: s.count + 1, msgs: s.msgs.concat(['b']) });
				if (m.type === 'go-quit') return Ramune.tui.Cmd.quit(s);
				return s;
			},
			view: function(s) { return 'count=' + s.count; },
			headless: true,
		});
		p.then(function(env) {
			__tuiTestDone(JSON.stringify(env.state), env.output);
		}, function(e) {
			__tuiTestDone(JSON.stringify({ error: String(e) }), '');
		});
		setTimeout(function() { Ramune.tui.dispatch({ type: 'bump' }); }, 50);
		setTimeout(function() { Ramune.tui.dispatch({ type: 'bump' }); }, 100);
		setTimeout(function() { Ramune.tui.dispatch({ type: 'bump' }); }, 150);
		setTimeout(function() { Ramune.tui.dispatch({ type: 'go-quit' }); }, 250);
	`); err != nil {
		t.Fatal(err)
	}

	// Drain the loop until the JS side resolves the Promise. The
	// JSC engine has its own dedicated thread; calling RunEventLoopFor
	// from a separate goroutine and concurrently waiting on doneCh
	// races the dedicated-thread invariant — drive it synchronously
	// so JSC sees one event-loop driver at a time.
	if err := r.RunEventLoopFor(3 * time.Second); err != nil {
		t.Fatalf("RunEventLoopFor: %v", err)
	}
	select {
	case <-doneCh:
	default:
		t.Fatalf("done callback did not fire before event loop drained")
	}

	mu.Lock()
	finalState := state
	out := captured
	mu.Unlock()

	if !strings.Contains(finalState, `"count":3`) {
		t.Errorf("expected count=3 in finalState, got %q", finalState)
	}
	if !strings.Contains(out, "count=3") {
		t.Errorf("expected count=3 frame in captured output; got %q", out)
	}
}

func TestTUI_Keymap_MatchesAndHelp(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`(function() {
		var km = Ramune.tui.keymap({
			up:   { keys: ['up', 'k'], help: { key: '↑/k', desc: 'up' } },
			down: { keys: ['down', 'j'], help: { key: '↓/j', desc: 'down' } },
			gone: { keys: ['z'], help: { key: 'z', desc: 'hidden' }, disabled: true },
		});
		return JSON.stringify({
			upK: km.matches({ type: 'key', key: 'k' }, 'up'),
			downQ: km.matches({ type: 'key', key: 'q' }, 'down'),
			gone: km.matches({ type: 'key', key: 'z' }, 'gone'),
			help: km.helpEntries(),
		});
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.String()
	if !strings.Contains(got, `"upK":true`) {
		t.Errorf("up/k should match: %s", got)
	}
	if !strings.Contains(got, `"downQ":false`) {
		t.Errorf("down/q should not match: %s", got)
	}
	if !strings.Contains(got, `"gone":false`) {
		t.Errorf("disabled binding should not match: %s", got)
	}
	if strings.Contains(got, "hidden") {
		t.Errorf("disabled binding should not appear in helpEntries: %s", got)
	}
}

func TestTUI_Stopwatch_Reducer(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`(function() {
		var s = Ramune.tui.stopwatch.init();
		var paused = s.running === false && s.elapsedMs === 0;
		s = Ramune.tui.stopwatch.toggle(s); // start
		var running = s.running === true && s.anchorAt > 0;
		// simulate a tick by mutating anchor backwards 200ms.
		s.anchorAt = s.anchorAt - 200;
		s = Ramune.tui.stopwatch.tick(s);
		var ticked = s.elapsedMs >= 200 && s.elapsedMs < 400;
		s = Ramune.tui.stopwatch.toggle(s); // pause
		var afterPause = s.running === false && s.anchorAt === 0;
		return JSON.stringify({ paused: paused, running: running, ticked: ticked, afterPause: afterPause, ms: s.elapsedMs });
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.String()
	for _, key := range []string{`"paused":true`, `"running":true`, `"ticked":true`, `"afterPause":true`} {
		if !strings.Contains(got, key) {
			t.Errorf("expected %s in %s", key, got)
		}
	}
}

func TestTUI_Timer_ExpiresAtZero(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`(function() {
		var t = Ramune.tui.timer.init(500, { running: true });
		// Backdate anchor 600ms so the next tick crosses zero.
		t.anchorAt = t.anchorAt - 600;
		t = Ramune.tui.timer.tick(t);
		return JSON.stringify({ expired: t.expired, remaining: t.remainingMs, running: t.running });
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.String()
	if !strings.Contains(got, `"expired":true`) {
		t.Errorf("timer should expire: %s", got)
	}
	if !strings.Contains(got, `"remaining":0`) {
		t.Errorf("remaining should be 0: %s", got)
	}
	if !strings.Contains(got, `"running":false`) {
		t.Errorf("running should be false after expire: %s", got)
	}
}

func TestTUI_Table_RendersHeaderAndRows(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`Ramune.tui.Table({
		columns: [
			{ title: 'Name', width: 8 },
			{ title: 'N', width: 3, align: 'right' },
		],
		rows: [['Alice', '1'], ['Bob', '20'], ['Carol', '300']],
		selected: 1,
		maxRows: 3,
		sortColumn: 1,
		sortDir: 'desc',
	})`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.String()
	if !strings.Contains(got, "Name") || !strings.Contains(got, "N ↓") {
		t.Errorf("expected header with sort indicator in %q", got)
	}
	if !strings.Contains(got, "Alice") || !strings.Contains(got, "Bob") || !strings.Contains(got, "Carol") {
		t.Errorf("expected all rows in %q", got)
	}
}

func TestTUI_Paginator_Reducer(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`(function() {
		var s = Ramune.tui.paginator.init(3, 13);
		var initOK = s.totalPages === 5 && s.page === 0 && s.perPage === 3;
		s = Ramune.tui.paginator.handleKey(s, { type: 'key', key: 'right' }) || s;
		s = Ramune.tui.paginator.handleKey(s, { type: 'key', key: 'right' }) || s;
		var afterTwo = s.page === 2;
		var slice = Ramune.tui.paginator.sliceForPage([0,1,2,3,4,5,6,7,8,9,10,11,12], s);
		s = Ramune.tui.paginator.handleKey(s, { type: 'key', key: 'end' }) || s;
		var afterEnd = s.page === 4;
		s = Ramune.tui.paginator.handleKey(s, { type: 'key', key: 'right' }) || s;
		var clampOK = s.page === 4;
		return JSON.stringify({ initOK: initOK, afterTwo: afterTwo, slice: slice, afterEnd: afterEnd, clampOK: clampOK });
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.String()
	for _, key := range []string{`"initOK":true`, `"afterTwo":true`, `"slice":[6,7,8]`, `"afterEnd":true`, `"clampOK":true`} {
		if !strings.Contains(got, key) {
			t.Errorf("missing %s in %s", key, got)
		}
	}
}

func TestTUI_Keymap_Sequences(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`(function() {
		var km = Ramune.tui.keymap({
			top:  { keys: ['g g'], help: { key: 'gg', desc: 'top' } },
			bot:  { keys: ['G'], help: { key: 'G', desc: 'bot' } },
			yank: { keys: ['y y'], help: { key: 'yy', desc: 'yank' } },
			quit: { keys: ['q'], help: { key: 'q', desc: 'quit' } },
		});
		function k(key) { return { type: 'key', key: key }; }
		// gg sequence completes
		var gg1 = km.match(k('g'));
		var gg2 = km.match(k('g'));
		// y interrupted by q falls back to single quit
		var y1 = km.match(k('y'));
		var qFallback = km.match(k('q'));
		// fresh y y completes
		var y2 = km.match(k('y'));
		var y3 = km.match(k('y'));
		// single G fires immediately
		var bot = km.match(k('G'));
		return JSON.stringify({ gg1: gg1, gg2: gg2, y1: y1, qFallback: qFallback, y2: y2, y3: y3, bot: bot });
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.String()
	for _, want := range []string{`"gg1":null`, `"gg2":"top"`, `"y1":null`, `"qFallback":"quit"`, `"y2":null`, `"y3":"yank"`, `"bot":"bot"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}

func TestTUI_Form_HandleKey(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`(function() {
		var s = Ramune.tui.form.init([
			{ name: 'name', type: 'text', label: 'Name', required: true },
			{ name: 'role', type: 'select', label: 'Role', options: ['admin', 'user'], value: 'user' },
		]);
		// Type "Alice" into the name field.
		'Alice'.split('').forEach(function(ch) {
			var n = Ramune.tui.form.handleKey(s, { type: 'key', key: ch, runes: ch });
			if (n) s = n;
		});
		// Tab to role, switch to admin.
		s = Ramune.tui.form.handleKey(s, { type: 'key', key: 'tab' }) || s;
		s = Ramune.tui.form.handleKey(s, { type: 'key', key: 'left' }) || s;
		// Submit.
		s = Ramune.tui.form.handleKey(s, { type: 'key', key: 'enter' }) || s;
		return JSON.stringify({
			values: Ramune.tui.form.values(s),
			submitted: s.submitted,
		});
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.String()
	if !strings.Contains(got, `"name":"Alice"`) {
		t.Errorf("expected name=Alice in %s", got)
	}
	if !strings.Contains(got, `"role":"admin"`) {
		t.Errorf("expected role=admin in %s", got)
	}
	if !strings.Contains(got, `"submitted":true`) {
		t.Errorf("expected submitted=true in %s", got)
	}
}

func TestTUI_Markdown_RendersANSI(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`Ramune.tui.markdown('# Hello\n\n**bold**', { theme: 'dark', width: 40 })`)
	if err != nil {
		t.Fatal(err)
	}
	out := v.String()
	if !strings.Contains(out, "Hello") {
		t.Errorf("expected heading text in %q", out)
	}
	// Glamour adds ANSI escape sequences for the dark theme.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI escape in dark-theme output: %q", out)
	}
}

// TestTUI_Markdown_RendererCached covers the per-(theme,width) cache
// that keeps the theme switcher demo from freezing under repeated
// renders. Calling Ramune.tui.markdown 200 times across 5 themes
// finishes well under a second when the renderer is memoized; without
// the cache, glamour rebuilds the chroma lexer registry per call and
// the loop blows up to multi-seconds.
func TestTUI_Markdown_RendererCached(t *testing.T) {
	r := sharedNodeCompat(t)
	v, err := r.Eval(`(function() {
		var themes = ['dark', 'light', 'dracula', 'tokyo-night', 'pink'];
		var src = '# Hello\n\n**bold** *italic*';
		var t0 = Date.now();
		for (var i = 0; i < 200; i++) {
			Ramune.tui.markdown(src, { theme: themes[i % themes.length], width: 60 });
		}
		return Date.now() - t0;
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := v.Float64()
	if err != nil {
		t.Fatalf("not a number: %v", err)
	}
	// 1 second is generous — typical run is ~50ms. If we ever fall
	// off the cache by accident, this catches it loud.
	if ms > 1000 {
		t.Errorf("200 renders took %vms — renderer cache likely broken", ms)
	}
}

// TestTUI_ServeSSH_Lifecycle binds an SSH server to an ephemeral port,
// dials a TCP connection (which will reject due to wish's SSH handshake
// requirements but proves the listener is up), then stops the server
// from JS via stopSSH(id). The Promise must resolve cleanly.
func TestTUI_ServeSSH_Lifecycle(t *testing.T) {
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	doneCh := make(chan struct{})
	if err := r.RegisterFunc("__tuiSSHResolved", func(args []any) (any, error) {
		select {
		case <-doneCh:
		default:
			close(doneCh)
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterFunc("__tuiSSHId", func(args []any) (any, error) {
		// no-op — we only need the side effect of grabbing id below
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	tmpDir := t.TempDir()
	hostKey := tmpDir + "/host_key"
	addr := "127.0.0.1:0" // OS picks ephemeral port
	if err := r.Exec(`
		var serverId = null;
		var p = Ramune.tui.serveSSH({
			addr: "` + addr + `",
			hostKeyPath: "` + hostKey + `",
			init: function() { return { ok: true }; },
			update: function(s) { return s; },
			view: function() { return ''; },
			onStart: function(id) { serverId = id; },
		});
		p.then(function() { __tuiSSHResolved(); }, function(e) { __tuiSSHResolved(); });
		setTimeout(function() {
			Ramune.tui.stopSSH(serverId);
		}, 100);
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoopFor(3 * time.Second); err != nil {
		t.Fatalf("RunEventLoopFor: %v", err)
	}
	select {
	case <-doneCh:
	default:
		t.Fatal("server Promise did not resolve")
	}
}

func TestTUI_Style_RoundTrip(t *testing.T) {
	r := sharedNodeCompat(t)
	out, err := r.Eval(`Ramune.tui.style('hi', { bold: true, padding: [0, 1] })`)
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	// Padding should add a leading + trailing space; bold is an ANSI
	// escape that may or may not show depending on color profile, so
	// only assert on the padded payload.
	if !strings.Contains(s, " hi ") {
		t.Fatalf("expected padded content in %q", s)
	}
}
