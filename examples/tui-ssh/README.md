# Ramune.tui over SSH (wish)

A Bubbletea counter served over SSH via Charm's
[wish](https://github.com/charmbracelet/wish). Each connection gets
its own counter — the same TSX `init`/`update`/`view` runs per session
inside one Ramune runtime.

## Run

```bash
cd examples/tui-ssh
../../ramune run server.tsx       # listens on :2222
# (or pass a port: ../../ramune run server.tsx 4422)
```

In another terminal, connect with any username (no auth required):

```bash
ssh -p 2222 \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null \
  anyone@localhost
```

The flags skip host-key validation since the demo generates an
ephemeral key on first run (`./host_key`). Reuse the same key across
restarts to avoid the "remote host key changed" warning. The server
side stays live as long as the Promise from `serveSSH` is pending —
hit `ctrl+c` to stop both the server and Ramune.

## How it works

- `Ramune.tui.serveSSH(opts)` wraps `wish.NewServer` + `bubbletea.MiddlewareWithProgramHandler`.
- For each SSH connection, the middleware calls our handler which
  invokes `init()` once for that connection's initial state, builds a
  fresh `tea.Program` with `WithInput(s) / WithOutput(s)` pinned to
  the SSH session, and starts the loop.
- `update`/`view` are dispatched into the JS runtime exactly like the
  local `Ramune.tui.run` flow — JSC serializes calls across all
  connections, so the per-connection isolation comes from the
  separate `tuiSession` state buffers, not from the JS layer.

## Caveats

- All connections share one Ramune runtime, so heavy `update` work
  blocks every other connection. Fine for typical TUI cadence;
  unsuitable for high-frequency mass deployments.
- No auth wired by default — tighten with `wish.WithPublicKeyAuth` or
  `wish.WithPasswordAuth` once we expose those options.
- TLS terminal size + window-resize events ride through the
  `bubbletea` middleware automatically; both reach the JS-side
  `update` as `{type:'resize', width, height}` msgs.
