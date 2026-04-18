# ramune-qjs-shim

QuickJS-NG → WebAssembly (WASI) bridge consumed by Ramune's `qjswasm` backend.

Compiled artifact (`quickjs.wasm`) is committed to the repo and embedded via
`//go:embed` in `engine_qjswasm.go`. End users do **not** need `wasi-sdk`
to run Ramune with `-tags qjswasm`; only contributors rebuilding the wasm
binary do.

## Rebuilding `quickjs.wasm`

### 1. Install wasi-sdk 27 (pinned)

The tarball extracts to `wasi-sdk-27.0-<arch>-<os>/` (not `wasi-sdk-27.0/`),
so point `WASI_SDK_PATH` at that full name. Installing under `$HOME` avoids
sudo entirely and matches what most contributors want.

```bash
# macOS arm64
curl -L https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-27/wasi-sdk-27.0-arm64-macos.tar.gz \
  | tar xz -C $HOME
export WASI_SDK_PATH=$HOME/wasi-sdk-27.0-arm64-macos

# macOS x86_64
curl -L https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-27/wasi-sdk-27.0-x86_64-macos.tar.gz \
  | tar xz -C $HOME
export WASI_SDK_PATH=$HOME/wasi-sdk-27.0-x86_64-macos

# linux x86_64
curl -L https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-27/wasi-sdk-27.0-x86_64-linux.tar.gz \
  | tar xz -C $HOME
export WASI_SDK_PATH=$HOME/wasi-sdk-27.0-x86_64-linux
```

To install under `/opt` (system-wide), split the pipe so `sudo` applies to
the extract as well — `sudo curl | tar` does not work because the pipe's
right side drops the sudo context:

```bash
curl -L https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-27/wasi-sdk-27.0-arm64-macos.tar.gz \
  -o /tmp/wasi-sdk.tar.gz
sudo tar xz -C /opt -f /tmp/wasi-sdk.tar.gz
sudo mv /opt/wasi-sdk-27.0-arm64-macos /opt/wasi-sdk
rm /tmp/wasi-sdk.tar.gz
```

### 2. Ensure QuickJS-NG submodule is populated

```bash
git submodule update --init --depth 1 third_party/quickjs-ng
```

### 3. Build

```bash
make -C third_party/ramune-qjs-shim      # or: make build-wasm-shim
```

The resulting `quickjs.wasm` is roughly 1–2 MB.

## Design

- `JSValue` is passed across the wasm boundary as `i64`. Build uses
  `-DJS_NAN_BOXING=1` so `JSValue` is a single `uint64_t`; no slab/handle
  table is needed.
- `(ptr, len)` return values are packed into one `i64` (`hi32 = ptr,
  lo32 = len`). The Go caller reads via `mod.Memory().Read` and releases
  via `rmn_free`.
- Go callbacks reach C via a single imported `env.go_dispatch` function.
  The JS trampoline JSON-encodes arguments, calls `go_dispatch`, and
  parses the response. Protocol matches the existing QuickJS (modernc)
  backend's `__goDispatch` so the Go-side implementation is reused.
- Exception propagation mirrors QuickJS: on error an exception-tagged
  `JSValue` is returned; the Go caller calls `get_exception` +
  `exception_to_json` to pull `{message, stack, name}`.

See `../../CLAUDE.md` (`qjswasm backend` section) for the complete
architecture overview.

## Pinned versions

| Tool       | Version            |
|------------|--------------------|
| wasi-sdk   | 27.0               |
| QuickJS-NG | `third_party/quickjs-ng` submodule commit `d35af9d` |
