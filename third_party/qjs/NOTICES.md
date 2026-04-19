# Notices for `third_party/qjs/`

This directory is a vendored fork of
[`github.com/fastschema/qjs`](https://github.com/fastschema/qjs) at `v0.0.6`
plus a two-line patch (`DisableFS` option in `options.go` / `runtime.go`) to
let callers opt out of the ambient WASI filesystem mount.

Two MIT-licensed projects ship from this directory:

- **fastschema/qjs** — the Go wrapper and C shim under `qjswasm/*.c`.
  License: `LICENSE` in this directory.
  Copyright (c) 2025 Nguyen Ngoc Phuong and Contributors.

- **QuickJS-NG** (<https://github.com/quickjs-ng/quickjs>) — compiled into
  the prebuilt `qjs.wasm` binary that lives in this directory. The source
  is not vendored (it was a git submodule in upstream that we did not
  initialize), so the LICENSE text is preserved at
  `qjswasm/quickjs/LICENSE` as the MIT notice required for distributing
  compiled QuickJS-NG code.
  Copyright (c) Fabrice Bellard, Charlie Gordon, Ben Noordhuis,
  Saúl Ibarra Corretgé, and contributors.

Both licenses permit redistribution as long as their copyright notices
accompany the binaries. Keeping the two `LICENSE` files in-tree (this one
and `qjswasm/quickjs/LICENSE`) satisfies that requirement.
