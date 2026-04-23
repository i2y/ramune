# @ramunejs/cli

A JS/TS runtime with soundness-gated AOT native compilation. Embeddable in Go, self-hostable as Cloudflare Workers-style handlers. This package ships the Ramune CLI via npm.

Project homepage: https://github.com/i2y/ramune

## Install

```
npm install -g @ramunejs/cli
```

The correct platform-specific binary is installed automatically via `optionalDependencies`.

## Supported platforms

| Platform      | Engine                |
| ------------- | --------------------- |
| macOS arm64   | JavaScriptCore (JIT)  |
| Linux x64     | QuickJS-NG on wazero  |
| Linux arm64   | QuickJS-NG on wazero  |
| Windows x64   | QuickJS-NG on wazero  |
| Windows arm64 | QuickJS-NG on wazero  |

Linux and Windows binaries use the pure-Go QuickJS-NG backend (zero host dependencies). For JavaScriptCore (JIT) throughput on Linux, install from source instead:

```
sudo apt install libjavascriptcoregtk-4.1-dev
go install github.com/i2y/ramune/cmd/ramune@latest
```

## macOS: JIT entitlement

JavaScriptCore's JIT requires the `com.apple.security.cs.allow-jit` entitlement. Binaries published here are ad-hoc code-signed with this entitlement in CI, so `ramune run` should work out of the box.

If you ever see `failed to allocate executable memory` or a similar JIT error, re-sign the local binary:

```
ramune setup-jit
```

## Usage

```
ramune run script.ts
ramune serve worker.ts
ramune eval "1 + 1"
ramune repl
ramune --help
```

See the main project README for the full command list, Workers-style API, Node/Bun compatibility notes, and embeddable-engine docs.

## License

MIT
