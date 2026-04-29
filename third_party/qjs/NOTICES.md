# Notices for `third_party/qjs/`

This directory is a vendored fork of
[`github.com/fastschema/qjs`](https://github.com/fastschema/qjs) at `v0.0.6`
plus local patches (see Ramune's `CLAUDE.md` → "Vendored fastschema/qjs
fork" for the full list — `DisableFS` option, CPU-timeout interrupt
handler wiring, and a WASI_SDK_PATH-portable Makefile).

The prebuilt `qjs.wasm` in this directory is a compiled artifact that
statically links code from several upstream projects. Each is attributed
below.

## Source-form components (Go + C shim)

- **fastschema/qjs** — the Go wrapper and C shim under `qjswasm/*.c`.
  License: MIT (`LICENSE` in this directory).
  Copyright (c) 2025 Nguyen Ngoc Phuong and Contributors.

## Compiled into `qjs.wasm`

- **QuickJS-NG** (<https://github.com/quickjs-ng/quickjs>) — the JS engine
  itself. The source is not vendored; the upstream QuickJS-NG submodule
  pin recorded in fastschema/qjs's `.gitmodules` is
  `d01ca4491fb24ccfeccb4c7394e28a3b21fd5986` (cloned locally only when
  rebuilding `qjs.wasm`).
  License: MIT (`qjswasm/quickjs/LICENSE`).
  Copyright (c) Fabrice Bellard, Charlie Gordon, Ben Noordhuis,
  Saúl Ibarra Corretgé, and contributors.

- **wasi-libc** (<https://github.com/WebAssembly/wasi-libc>) — provides
  the C standard library (memcpy / malloc / printf / fwrite / file
  helpers) statically linked into `qjs.wasm` via wasi-sdk. wasi-libc is
  multi-licensed under (a) the Apache License v2.0 with LLVM Exceptions,
  (b) the Apache License v2.0, or (c) the MIT License — Ramune elects
  the MIT option. wasi-libc itself incorporates code from third-party
  projects under their original licenses (musl libc — MIT; dlmalloc —
  CC0; emmalloc — MIT; cloudlibc — BSD-2-Clause; musl-fts —
  BSD-3-Clause); these notices are reproduced inline in the wasi-libc
  source tree and propagate via the multi-license. The wasi-libc LICENSE
  texts are preserved at `qjswasm/LICENSE-wasi-libc.txt`.
  Pinned commit (matches wasi-sdk-25): `574b88da4815`.

- **LLVM `compiler-rt`** (<https://github.com/llvm/llvm-project>) —
  builtins (e.g. integer division, floating-point support routines) that
  the wasi-sdk toolchain links into `qjs.wasm` when the QuickJS-NG /
  wasi-libc sources require them. License: Apache License v2.0 with LLVM
  Exceptions. The full license text is preserved at
  `qjswasm/LICENSE-llvm.txt`.
  Pinned LLVM version (matches wasi-sdk-25): `19.1.5`.

## Election of license terms

Where a multi-license is offered (wasi-libc), Ramune elects the **MIT
License** option for redistribution — this is the least-restrictive
common denominator and matches the licensing posture of the other
components in this directory.

For Apache-2.0 with LLVM Exceptions (LLVM `compiler-rt`), the LLVM
Exception is what makes static linkage into a downstream binary
practical: it explicitly waives the source-disclosure / NOTICE-file
propagation that vanilla Apache-2.0 would otherwise require for
"Object Form" inclusion. Bundling the LICENSE text with `qjs.wasm`
(via this NOTICES.md and `qjswasm/LICENSE-llvm.txt`) discharges the
remaining obligation.

## How to keep this current

- When rebuilding `qjs.wasm` against a newer wasi-sdk: re-check the
  `wasi-libc` and `llvm` versions that the new wasi-sdk pins (the
  `share/wasi-sysroot/VERSION` file in the install records both) and
  bump the pin commits / version strings recorded above.
- When rebasing onto a newer fastschema/qjs or QuickJS-NG: bump the
  version strings + commit pins above accordingly.
- The license-text files (`qjswasm/LICENSE-wasi-libc.txt`,
  `qjswasm/LICENSE-llvm.txt`) ship with Ramune so consumers receive the
  attribution required by Apache-2.0 / MIT without needing to fetch
  upstream.
