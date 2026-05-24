import React from "react";
import {
  RAMUNE_BLUE, RAMUNE_AQUA, RAMUNE_INK,
  useLang, tr,
  Marble, RamuneBottle, LangToggle, CodeBlock, sy, BubbleField,
} from "./shared.jsx";

const GITHUB_URL = "https://github.com/i2y/ramune";
const README_URL = `${GITHUB_URL}#readme`;

function smoothScrollTo(hash) {
  const el = typeof document !== "undefined" ? document.querySelector(hash) : null;
  if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
}

function NavLink({ href, children }) {
  const internal = href.startsWith("#");
  const onClick = internal
    ? (e) => { e.preventDefault(); smoothScrollTo(href); }
    : undefined;
  return (
    <a
      href={href}
      onClick={onClick}
      target={internal ? undefined : "_blank"}
      rel={internal ? undefined : "noopener"}
      style={{ color: "inherit", textDecoration: "none", cursor: "pointer" }}
    >{children}</a>
  );
}

function VariantB() {
  const { lang } = useLang();

  return (
    <div style={{
      width: "100%", minHeight: "100%",
      background: "#ffffff", color: RAMUNE_INK,
      fontFamily: "Inter, system-ui, sans-serif",
      position: "relative", overflow: "hidden",
    }}>
      <div style={{
        position: "absolute", top: 0, left: 0, right: 0, height: 640,
        background: `linear-gradient(180deg, #eef5ff 0%, #f7fbff 40%, transparent 100%)`,
        pointerEvents: "none",
      }}/>
      <div style={{
        position: "absolute", top: -180, right: -140, width: 620, height: 620,
        borderRadius: "50%",
        background: `radial-gradient(circle, ${RAMUNE_AQUA}22, transparent 70%)`,
        pointerEvents: "none",
      }}/>
      <div style={{ position: "absolute", top: 60, right: 80, width: 380, height: 500, pointerEvents: "none", opacity: .55 }}>
        <BubbleField count={18} blue={RAMUNE_AQUA}/>
      </div>

      {/* Nav */}
      <nav style={{
        position: "relative", zIndex: 10, padding: "22px 56px",
        display: "flex", alignItems: "center", gap: 24,
        borderBottom: "1px solid rgba(10,22,40,.06)",
      }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <img src="assets/ramune.png" alt="Ramune logo" style={{ width: 32, height: 32, objectFit: "contain" }}/>
          <span style={{ fontSize: 18, fontWeight: 700, letterSpacing: "-.02em", fontFamily: "Inter Tight" }}>Ramune</span>
          <span style={{
            fontFamily: "JetBrains Mono, monospace", fontSize: 10.5,
            color: "rgba(10,22,40,.45)", marginLeft: 2,
            padding: "2px 6px", borderRadius: 4, background: "rgba(10,22,40,.04)",
          }}>JS/TS × Go</span>
        </div>
        <div style={{ flex: 1 }}/>
        <div style={{ display: "flex", gap: 24, fontSize: 13.5, fontWeight: 500, color: "rgba(10,22,40,.7)" }}>
          <NavLink href="#quickstart">CLI</NavLink>
          <NavLink href="#backends">{tr("Embed", "埋め込み")}</NavLink>
          <NavLink href="#hybrid">Hybrid</NavLink>
          <NavLink href="https://github.com/i2y/ramune/tree/main/examples/workers">Workers</NavLink>
          <NavLink href={README_URL}>{tr("Reference", "リファレンス")}</NavLink>
        </div>
        <LangToggle/>
        <a href={GITHUB_URL} target="_blank" rel="noopener" style={{
          padding: "7px 14px", borderRadius: 8, border: "1px solid rgba(10,22,40,.12)",
          background: "#fff", fontSize: 13, fontWeight: 600, cursor: "pointer",
          color: RAMUNE_INK, textDecoration: "none",
          display: "inline-flex", alignItems: "center",
        }}>GitHub ↗</a>
      </nav>

      {/* Hero */}
      <section style={{ position: "relative", zIndex: 2, padding: "64px 56px 56px", maxWidth: 1280, margin: "0 auto" }}>
        <a href="#quickstart" onClick={(e) => { e.preventDefault(); smoothScrollTo("#quickstart"); }} style={{
          display: "inline-flex", alignItems: "center", gap: 10,
          padding: "5px 12px",
          background: "#fff", border: `1px solid ${RAMUNE_BLUE}33`,
          borderRadius: 999, fontSize: 12, color: "rgba(10,22,40,.8)",
          marginBottom: 28, marginTop: 8, textDecoration: "none",
          boxShadow: `0 2px 10px -2px ${RAMUNE_BLUE}22`,
        }}>
          <span>{tr(
            "Embed in Go · Self-host Workers · npm CLI",
            "Go に組み込む · セルフホスト Workers · npm CLI"
          )}</span>
          <span style={{ color: RAMUNE_BLUE }}>→</span>
        </a>

        <h1 style={{
          fontFamily: "Inter Tight, Inter, sans-serif",
          fontSize: 68, lineHeight: 1.02, letterSpacing: "-.035em",
          fontWeight: 600, margin: "0 0 20px", maxWidth: 960,
          textWrap: "balance",
        }}>
          {tr(
            "A JS/TS runtime with soundness-gated AOT native compilation.",
            "健全性が証明された型付き関数を AOT で native Go にコンパイルする JS/TS ランタイム。"
          )}
        </h1>
        <p style={{
          fontSize: 18, lineHeight: 1.55, color: "rgba(10,22,40,.65)",
          maxWidth: 760, margin: "0 0 32px", textWrap: "pretty",
        }}>
          {tr(
            "Three backends behind one API — JSC+JIT on macOS/Linux, pure-Go portability to Windows (QuickJS-NG on wazero, goja). Embed in Go, or self-host Cloudflare Workers-style handlers on your own infrastructure.",
            "3 バックエンドを 1 つの API で。macOS/Linux は JSC+JIT、Windows は純 Go (QuickJS-NG on wazero, goja)。Go に組み込むか、Cloudflare Workers 形式のハンドラを自前インフラでセルフホスト。"
          )}
        </p>

        <div style={{ display: "flex", alignItems: "center", gap: 14, marginBottom: 12 }}>
          <a href={README_URL} target="_blank" rel="noopener" style={{
            padding: "12px 20px", borderRadius: 10, border: "none",
            background: RAMUNE_INK, color: "#fff", fontSize: 14, fontWeight: 600,
            cursor: "pointer", display: "inline-flex", alignItems: "center", gap: 10,
            boxShadow: "0 8px 24px -10px rgba(10,22,40,.6)",
            textDecoration: "none",
          }}>
            <Marble size={18}/>
            {tr("install · quickstart", "インストール · クイックスタート")}
          </a>
          <button onClick={() => smoothScrollTo("#bench")} style={{
            padding: "12px 18px", borderRadius: 10, border: "1px solid rgba(10,22,40,.12)",
            background: "#fff", color: RAMUNE_INK, fontSize: 14, fontWeight: 600,
            cursor: "pointer", fontFamily: "JetBrains Mono, monospace",
          }}>
            {tr("Benchmarks", "ベンチマーク")} ↓
          </button>
          <div style={{
            fontFamily: "JetBrains Mono, monospace", fontSize: 11.5,
            color: "rgba(10,22,40,.45)", marginLeft: 6,
          }}>MIT · npm · Go 1.26+ · TypeScript 7 Beta (tsgo)</div>
        </div>

        <div style={{
          display: "grid", gridTemplateColumns: "repeat(5, 1fr)", gap: 14,
          marginTop: 48,
        }}>
          {[
            { n: "01", t: tr("Run TS from CLI","CLIでTS実行"), d: tr("npm i -g @ramunejs/cli → ramune run hello.ts. One binary with run · test · compile · check · fmt · lint · repl, --sandbox, and --docker for untrusted code. Go toolchain optional.","npm i -g @ramunejs/cli → ramune run hello.ts。run · test · compile · check · fmt · lint · repl がバイナリ 1 本、--sandbox / --docker で信頼できないコードも扱える。Go 環境は任意。"), h: "cli", href: "#quickstart" },
            { n: "02", t: tr("Sandbox & permissions","サンドボックスと権限"), d: tr("Layered: language-level deny-all + allow-lists on every backend, plus WASM VM isolation via wazero when built with -tags qjswasm. Docker is an optional extra layer.","多層: どのバックエンドでも言語層の deny-all + allow-list、-tags qjswasm ビルドなら wazero の WASM VM 隔離も追加。Docker は任意の追加層。"), h: "sandbox", href: "#quickstart" },
            { n: "03", t: tr("Embed in Go","Go に埋め込む"), d: tr("Call any Go lib from JS. Same API across all three backends, zero Cgo at build time.","GoライブラリをJSから呼出し、3バックエンド共通API、ビルド時Cgo不要。"), h: "embed", href: "#backends" },
            { n: "04", t: tr("Self-host Workers","Workersセルフホスト"), d: tr("export default { fetch }. Compile handler + runtime into a single Go binary; run on your VM, bare metal, or scratch container.","export default { fetch }。ハンドラとランタイムを 1 つの Go バイナリに、自分の VM / bare metal / scratch コンテナで実行。"), h: "workers", href: "https://github.com/i2y/ramune/tree/main/examples/workers" },
            { n: "05", t: tr("Terminal UI","ターミナルUI"), d: tr("Ramune.tui: Bubbletea + Lipgloss + glamour, authored in TSX. Elm-style init/update/view; components, block layout, chord keymap; runs locally or over SSH via wish.","Ramune.tui: Bubbletea + Lipgloss + glamour を TSX で記述。Elm 風 init/update/view、コンポーネント・ブロックレイアウト・和音キーマップ。ローカル実行と wish 経由 SSH 提供。"), h: "tui", href: "https://github.com/i2y/ramune/tree/main/examples" },
          ].map((p) => {
            const isInternal = p.href.startsWith("#");
            return (
              <a key={p.n} id={p.h}
                href={p.href}
                onClick={isInternal ? (e) => { e.preventDefault(); smoothScrollTo(p.href); } : undefined}
                target={isInternal ? undefined : "_blank"}
                rel={isInternal ? undefined : "noopener"}
                style={{
                  padding: "20px 22px 24px", borderRadius: 14,
                  border: "1px solid rgba(10,22,40,.08)", background: "#fff",
                  position: "relative", cursor: "pointer",
                  transition: "all .18s",
                  scrollMarginTop: 80,
                  display: "block", textDecoration: "none", color: "inherit",
                }}>
                <div style={{
                  display: "flex", justifyContent: "space-between", marginBottom: 16,
                  fontFamily: "JetBrains Mono, monospace", fontSize: 11,
                  color: "rgba(10,22,40,.4)",
                }}>
                  <span>{p.n}</span>
                  <span style={{ color: RAMUNE_BLUE }}>{isInternal ? "↓" : "↗"}</span>
                </div>
                <div style={{ fontSize: 16, fontWeight: 600, marginBottom: 8, letterSpacing: "-.01em" }}>{p.t}</div>
                <div style={{ fontSize: 13, lineHeight: 1.5, color: "rgba(10,22,40,.6)" }}>{p.d}</div>
              </a>
            );
          })}
        </div>
      </section>

      <QuickstartSection/>

      {/* Backend comparison table */}
      <section id="backends" style={{ padding: "40px 56px 40px", maxWidth: 1280, margin: "0 auto", scrollMarginTop: 80 }}>
        <SectionEyebrow num="II">{tr("Three backends, one API", "3 バックエンド / 1 API")}</SectionEyebrow>
        <h2 style={{
          fontFamily: "Inter Tight", fontSize: 40, letterSpacing: "-.025em",
          fontWeight: 600, margin: "10px 0 8px",
        }}>{tr("Pick your tradeoff at build time.", "ビルド時にトレードオフを選ぶ。")}</h2>
        <p style={{ fontSize: 15, color: "rgba(10,22,40,.6)", maxWidth: 720, margin: "0 0 22px" }}>
          {tr(
            "Handler code and Go interop stay identical. The same build tag picks the backend everywhere — install the CLI, compile a single-binary app, or embed the runtime in your own Go program.",
            "ハンドラコードとGoのinteropは変更なし。同じビルドタグがどの経路でも効く — CLI の install・単一バイナリコンパイル・自前 Go プログラムへの組み込み、どれでも同じ切替方法。"
          )}
        </p>
        <div style={{
          marginBottom: 32, padding: "16px 18px", borderRadius: 10,
          background: "#0B1B3A", color: "#cfe3ff",
          fontFamily: "JetBrains Mono, ui-monospace, monospace",
          fontSize: 12.5, lineHeight: 1.7,
          border: "1px solid rgba(255,255,255,.08)",
          boxShadow: "0 10px 30px -12px rgba(10,30,70,.35)",
        }}>
          <div style={{ color: "#5e7a9b", fontStyle: "italic" }}>{tr("# CLI install", "# CLI の install 時")}</div>
          <div>$ <span style={{ color: "#7ad9ff" }}>go install</span> <span style={{ color: "#ff7ab6" }}>-tags</span> qjswasm github.com/i2y/ramune/cmd/ramune@latest</div>
          <div style={{ marginTop: 8, color: "#5e7a9b", fontStyle: "italic" }}>{tr("# single-binary compile","# 単一バイナリコンパイル")}</div>
          <div>$ <span style={{ color: "#7ad9ff" }}>ramune</span> compile app.ts <span style={{ color: "#ff7ab6" }}>--tags</span> qjswasm <span style={{ color: "#ff7ab6" }}>-o</span> myapp</div>
          <div style={{ marginTop: 8, color: "#5e7a9b", fontStyle: "italic" }}>{tr("# embed in your Go program","# Go プログラムへの組み込み")}</div>
          <div>$ <span style={{ color: "#7ad9ff" }}>go build</span> <span style={{ color: "#ff7ab6" }}>-tags</span> qjswasm ./...</div>
        </div>
        <BackendTable/>
      </section>

      <HybridSection/>

      {/* Benchmark bars */}
      <section id="bench" style={{ padding: "40px 56px 80px", maxWidth: 1280, margin: "0 auto", scrollMarginTop: 80 }}>
        <SectionEyebrow num="IV">{tr("Performance", "パフォーマンス")}</SectionEyebrow>
        <h2 style={{
          fontFamily: "Inter Tight", fontSize: 40, letterSpacing: "-.025em",
          fontWeight: 600, margin: "10px 0 8px",
        }}>{tr("60× over goja on CPU. 62k req/s on multi-runtime pool.", "CPU で goja 比 60×。multi-runtime pool で 62k req/s。")}</h2>
        <p style={{ fontSize: 15, color: "rgba(10,22,40,.6)", maxWidth: 720, margin: "0 0 36px" }}>
          {tr(
            "Apple M4 Max, JSC backend (the default). Fib(35) vs other Go-embedded JS runtimes and RuntimePool req/s scaling on a JSON handler (wrk -t4 -c100 -d10s). Reproduce with make bench-go.",
            "Apple M4 Max、JSC バックエンド (デフォルト)。Fib(35) の他 Go 組み込み JS ランタイム比較と、JSON ハンドラでの RuntimePool req/s スケーリング (wrk -t4 -c100 -d10s)。make bench-go で再現可能。"
          )}
        </p>

        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 48 }}>
          <BenchChart title={tr("Fibonacci(35) · ms, lower better", "Fibonacci(35) · ms, 低いほど良い")}
            bars={[
              { l: "Ramune JSC+JIT", v: 35, label: "35 ms", hi: true },
              { l: "Ramune qjswasm", v: 1987, label: "1,987 ms" },
              { l: "Ramune goja", v: 2400, label: "2,400 ms" },
              { l: "otto", v: 26413, label: "26,413 ms" },
            ]} max={26413}/>
          <BenchChart title={tr("RuntimePool JSC · req/s, higher better","RuntimePool JSC · req/s, 高いほど良い")}
            bars={[
              { l: "1 worker", v: 40511, label: "40,511" },
              { l: "2 workers", v: 54500, label: "54,500" },
              { l: "3 workers", v: 58401, label: "58,401" },
              { l: "4 workers", v: 59706, label: "59,706" },
              { l: "5 workers", v: 60913, label: "60,913" },
              { l: "6 workers", v: 62407, label: "62,407", hi: true },
            ]} max={64000}/>
        </div>
        <p style={{ fontSize: 13, color: "rgba(10,22,40,.5)", maxWidth: 720, margin: "20px 0 0", lineHeight: 1.55 }}>
          {tr(
            "Pure-Go scaling story: qjswasm goes 2,348 → 13,435 req/s from 1 → 6 workers (5.72×, near-linear), the best multiplicative scaling among pure-Go backends. JSC wins on absolute throughput; qjswasm wins on scaling factor and Windows/scratch-image portability.",
            "純 Go 系のスケーリングは qjswasm が 1→6 workers で 2,348 → 13,435 req/s (5.72× ほぼ線形) と純 Go 系では最も良い。絶対値は JSC、スケーリング係数と Windows/scratch イメージ対応は qjswasm。"
          )}
        </p>

        <div style={{ marginTop: 56, paddingTop: 32, borderTop: "1px solid rgba(10,22,40,.06)" }}>
          <div style={{
            fontSize: 11, fontFamily: "JetBrains Mono, monospace",
            color: RAMUNE_BLUE, fontWeight: 600, letterSpacing: ".08em",
            textTransform: "uppercase", marginBottom: 14,
          }}>{tr("Hybrid extraction · ramune compile --hybrid", "Hybrid 抽出 · ramune compile --hybrid")}</div>
          <BenchChart
            title={tr("fib(30) · ms, lower better (examples/hybrid/)", "fib(30) · ms, 低いほど良い (examples/hybrid/)")}
            bars={[
              { l: "qjswasm · JS-only", v: 185.8, label: "185.8 ms" },
              { l: "qjswasm · --hybrid", v: 2.0, label: "2.0 ms", hi: true, badge: "92×" },
              { l: "JSC+JIT · JS-only", v: 3.1, label: "3.1 ms" },
              { l: "JSC+JIT · --hybrid", v: 2.0, label: "2.0 ms", badge: "1.55×" },
            ]} max={186}
          />
          <p style={{ fontSize: 13, color: "rgba(10,22,40,.55)", maxWidth: 760, margin: "20px 0 0", lineHeight: 1.6 }}>
            {tr(
              "The picker proves semantic equivalence, not speed. On qjswasm (no JS JIT), recursion-heavy kernels like fib go near-native — 92× here. On JSC+JIT the JIT already specialises integer-typed JS aggressively, so hybrid is a targeted tool: some kernels regress (countPrimes 60× slower when integer modulo dominates; sumSquares over a 1000-element array 103× slower when marshalling dominates). Rule of thumb: extract recursive / loop kernels with primitive arguments; leave array-arg APIs and tight methods on the JS floor. Run with --hybrid-report to see what got extracted and measure.",
              "picker は意味等価性を証明するだけで、速度は保証しない。qjswasm (JIT 無し) では fib のような再帰カーネルがここでは 92× とほぼネイティブ相当。JSC+JIT では JIT が既に整数型付き JS を最適化しているので、hybrid はピンポイントで使う — 整数 modulo 支配の countPrimes は 60× 遅化、1000 要素配列引数の sumSquares は marshal コストが支配して 103× 遅化する。基本的には再帰 / ループ系でプリミティブ引数のカーネルを抽出し、配列引数 API や呼出頻度の高いメソッドは JS フロアに残す。--hybrid-report で抽出結果を確認して必ず計測する。"
            )}
          </p>

          <div style={{ marginTop: 36 }}>
            <BenchChart
              title={tr("Fibonacci(40) wall-clock vs Bun / Node — Apple M4 Max, hyperfine (bench/run.sh)", "Fibonacci(40) wall-clock vs Bun / Node — Apple M4 Max, hyperfine (bench/run.sh)")}
              bars={[
                { l: "node fib.js", v: 535, label: "535 ms" },
                { l: "bun run fib.js", v: 364, label: "364 ms" },
                { l: "compile (JS)", v: 358, label: "358 ms" },
                { l: "compile --hybrid", v: 243, label: "243 ms", hi: true, badge: "1.5×" },
              ]} max={550}
            />
            <p style={{ fontSize: 13, color: "rgba(10,22,40,.55)", maxWidth: 760, margin: "20px 0 0", lineHeight: 1.6 }}>
              {tr(
                "Wall-clock CLI bench against the Bun and Node binaries on the same source — fib(40) recursive numerics. ramune compile --hybrid lands at 243 ms vs 358 ms for the JS-only compile, 364 ms for Bun, 535 ms for Node. Compute-only (subtract ~16 ms binary startup): 227 ms vs 357 ms (Bun) and 519 ms (Node) — Bun's startup advantage from the Zig launcher fades once the workload dominates. The extracted Go is what the picker emits verbatim; this is the same win available to any TS app whose hot kernel is recursive or loop-heavy with primitive arguments.",
                "同じソースから Bun / Node とコンパイル済バイナリを比較する wall-clock ベンチ — fib(40) 再帰数値計算。ramune compile --hybrid は 243 ms、JS-only compile は 358 ms、Bun は 364 ms、Node は 535 ms。バイナリ起動 ~16 ms を差し引いた compute-only: hybrid 227 ms、Bun 357 ms、Node 519 ms — Bun の Zig launcher による起動の優位は計算時間が支配する局面では消える。抽出される Go コードは picker が emit するものと同一であり、再帰 / ループ系で primitive 引数の hot kernel を持つ任意の TS アプリでこの効果が得られる。"
              )}
            </p>
          </div>

          <div style={{ marginTop: 36, paddingTop: 24, borderTop: "1px solid rgba(10,22,40,.06)" }}>
            <div style={{
              fontSize: 11, fontFamily: "JetBrains Mono, monospace",
              color: RAMUNE_BLUE, fontWeight: 600, letterSpacing: ".08em",
              textTransform: "uppercase", marginBottom: 14,
            }}>{tr("TinyGo standalone WASM · ramune compile --target wasm-wasi", "TinyGo 単一 WASM · ramune compile --target wasm-wasi")}</div>
            <p style={{ fontSize: 13, color: "rgba(10,22,40,.55)", maxWidth: 760, margin: "0 0 16px", lineHeight: 1.6 }}>
              {tr(
                "The same picker pipeline can target TinyGo instead of go, producing a self-contained WASI reactor (.wasm, ~110-320 KB) of the extracted Go alone — no JS runtime, no ramune host bundled. Numerics-only signatures get //go:wasmexport wrappers callable from any wasm host (wazero, wasmtime, browser-via-wasi-shim) after _initialize. Verified: add(2.5, 3.25)=5.75 and fib(10)=55 callable from a Go host through wazero's ExportedFunction lookup.",
                "同じ picker パイプラインで go の代わりに TinyGo をターゲットにすると、抽出された Go だけで完結する単一 WASI reactor (.wasm、~110-320 KB) が生成できる — JS ランタイムも ramune ホストもバンドルしない。数値型のみのシグネチャには //go:wasmexport ラッパーが付き、wazero / wasmtime / browser-via-wasi-shim など任意の WASM ホストから _initialize 後に呼び出せる。検証済み: wazero の ExportedFunction 経由で add(2.5, 3.25)=5.75 と fib(10)=55 が動作。"
              )}
            </p>
            <CodeBlock
              tabs={[
                { name: "CLI", code: `$ ${sy.k("ramune")} compile ${sy.s("--target wasm-wasi")} ${sy.s("-o")} app.wasm app.ts
${sy.c("# → 320 KB self-contained WASI reactor, no JS runtime")}
${sy.c("# → exports (call after _initialize):")}
${sy.c("#     fib(float64) float64")}` },
              ]}
            />
          </div>
        </div>
      </section>

      {/* Built-on strip */}
      <section style={{
        padding: "16px 56px 40px", maxWidth: 1280, margin: "0 auto", textAlign: "center",
      }}>
        <div style={{
          fontSize: 10.5, fontFamily: "JetBrains Mono, monospace",
          color: "rgba(10,22,40,.4)", letterSpacing: ".12em",
          textTransform: "uppercase", marginBottom: 14, fontWeight: 600,
        }}>
          {tr("Built on", "スタック")}
        </div>
        <div style={{
          fontSize: 13, color: "rgba(10,22,40,.6)", lineHeight: 1.9,
          fontFamily: "JetBrains Mono, monospace",
        }}>
          JavaScriptCore · QuickJS-NG · goja · wazero · fastschema/qjs · purego · esbuild · typescript-go · rslint
        </div>
      </section>

      {/* Footer */}
      <footer style={{
        position: "relative", padding: "56px 56px 48px",
        borderTop: "1px solid rgba(10,22,40,.06)",
        background: "#fafcff", overflow: "hidden",
      }}>
        <div style={{
          position: "absolute", right: -20, top: -40, opacity: .12,
          transform: "rotate(8deg)", pointerEvents: "none",
        }}>
          <RamuneBottle width={140} height={360} withMarble={true} fizzy={false}/>
        </div>
        <div style={{ maxWidth: 1280, margin: "0 auto", display: "flex", alignItems: "flex-start", gap: 48, position: "relative" }}>
          <div style={{ maxWidth: 420 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 14 }}>
              <img src="assets/ramune.png" alt="" style={{ width: 28, height: 28, objectFit: "contain" }}/>
              <span style={{ fontSize: 16, fontWeight: 700, letterSpacing: "-.02em", fontFamily: "Inter Tight" }}>Ramune</span>
            </div>
            <div style={{ fontSize: 13, color: "rgba(10,22,40,.55)", lineHeight: 1.55 }}>
              {tr(
                "A JS/TS runtime with soundness-gated AOT native compilation. Embed it in Go, or install via npm. Three backends behind one API (JSC+JIT, QuickJS-NG on wazero, goja), self-hosted Workers-style modules.",
                "健全性ゲート付き AOT native コンパイルを備えた JS/TS ランタイム。Go に組み込むか npm で入れる。3 バックエンド (JSC+JIT / QuickJS-NG on wazero / goja) を 1 つの API で、セルフホスト Workers 対応。"
              )}
            </div>
          </div>
          <div style={{ flex: 1 }}/>
          <div style={{ display: "flex", flexDirection: "column", alignItems: "flex-end", gap: 18 }}>
            <div>
              <div style={{
                fontSize: 10.5, fontFamily: "JetBrains Mono, monospace",
                color: "rgba(10,22,40,.5)", letterSpacing: ".12em",
                textTransform: "uppercase", fontWeight: 600,
                textAlign: "right", marginBottom: 8,
              }}>
                {tr("Built on Ramune", "Ramune ベース")}
              </div>
              <div style={{
                fontSize: 12, color: "rgba(10,22,40,.65)", lineHeight: 1.9,
                fontFamily: "JetBrains Mono, monospace", textAlign: "right",
              }}>
                {[
                  { name: "Dark", url: "https://github.com/i2y/dark", tag: tr("Go SSR · Preact · htmx · WebView","Go SSR · Preact · htmx · WebView") },
                ].map((c) => (
                  <div key={c.name}>
                    <span style={{ color: "rgba(10,22,40,.35)" }}>{c.tag} · </span>
                    <a href={c.url} target="_blank" rel="noopener" style={{
                      color: "rgba(10,22,40,.8)", textDecoration: "none", fontWeight: 500,
                    }}>{c.name} ↗</a>
                  </div>
                ))}
              </div>
            </div>
            <div>
              <div style={{
                fontSize: 10.5, fontFamily: "JetBrains Mono, monospace",
                color: "rgba(10,22,40,.5)", letterSpacing: ".12em",
                textTransform: "uppercase", fontWeight: 600,
                textAlign: "right", marginBottom: 8,
              }}>
                {tr("Credits · thank you", "クレジット · ありがとうございます")}
              </div>
              <div style={{
                fontSize: 12, color: "rgba(10,22,40,.65)", lineHeight: 1.9,
                fontFamily: "JetBrains Mono, monospace", textAlign: "right",
              }}>
                {[
                  { name: "JavaScriptCore",  url: "https://webkit.org/",                          tag: tr("engine · JIT","エンジン · JIT") },
                  { name: "QuickJS-NG",      url: "https://github.com/quickjs-ng/quickjs",        tag: tr("engine · wasm","エンジン · wasm") },
                  { name: "goja",            url: "https://github.com/dop251/goja",               tag: tr("engine · pure Go","エンジン · pure Go") },
                  { name: "wazero",          url: "https://github.com/tetratelabs/wazero",        tag: tr("wasm runtime","wasm runtime") },
                  { name: "fastschema/qjs",  url: "https://github.com/fastschema/qjs",            tag: tr("qjs wrapper","qjs wrapper") },
                  { name: "purego",          url: "https://github.com/ebitengine/purego",         tag: tr("Cgo-free FFI","Cgo-free FFI") },
                  { name: "esbuild",         url: "https://github.com/evanw/esbuild",             tag: tr("bundler","バンドラ") },
                  { name: "typescript-go",   url: "https://github.com/microsoft/typescript-go",   tag: tr("checker · fmt · emit","check · fmt · emit") },
                  { name: "rslint",          url: "https://github.com/web-infra-dev/rslint",      tag: tr("linter","lint") },
                  { name: "Bubbletea",       url: "https://github.com/charmbracelet/bubbletea",   tag: tr("TUI framework","TUI フレームワーク") },
                  { name: "Lipgloss",        url: "https://github.com/charmbracelet/lipgloss",    tag: tr("TUI styling","TUI スタイリング") },
                  { name: "glamour",         url: "https://github.com/charmbracelet/glamour",     tag: tr("Markdown render","Markdown レンダリング") },
                  { name: "wish",            url: "https://github.com/charmbracelet/wish",        tag: tr("SSH server","SSH サーバ") },
                ].map((c) => (
                  <div key={c.name}>
                    <span style={{ color: "rgba(10,22,40,.35)" }}>{c.tag} · </span>
                    <a href={c.url} target="_blank" rel="noopener" style={{
                      color: "rgba(10,22,40,.8)", textDecoration: "none", fontWeight: 500,
                    }}>{c.name} ↗</a>
                  </div>
                ))}
              </div>
            </div>
            <div style={{ fontFamily: "JetBrains Mono, monospace", fontSize: 11.5, color: "rgba(10,22,40,.5)" }}>
              MIT · © 2026 · <a href={GITHUB_URL} target="_blank" rel="noopener" style={{ color: "inherit", textDecoration: "none" }}>github.com/i2y/ramune</a>
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
}

function SectionEyebrow({ num, children }) {
  return (
    <div style={{
      display: "flex", alignItems: "center", gap: 10,
      fontFamily: "JetBrains Mono, monospace", fontSize: 11,
      color: RAMUNE_BLUE, fontWeight: 600, letterSpacing: ".08em",
      textTransform: "uppercase",
    }}>
      <span style={{ color: "rgba(10,22,40,.3)" }}>§ {num}</span>
      <span style={{ flex: "0 0 24px", height: 1, background: RAMUNE_BLUE, opacity: .4 }}/>
      <span>{children}</span>
    </div>
  );
}

function BackendTable() {
  const rows = [
    { k: tr("Engine","エンジン"), jsc: "JavaScriptCore (purego)", qjs: "QuickJS-NG on wazero", goja: "dop251/goja" },
    { k: "JIT", jsc: "✓", qjs: "—", goja: "—" },
    { k: tr("Platforms","対応OS"), jsc: "macOS · Linux", qjs: "macOS · Linux · Windows", goja: "macOS · Linux · Windows" },
    { k: tr("System deps","システム依存"), jsc: tr("mac: none · linux: libjsc","mac: 無 · linux: libjsc"), qjs: tr("none","無"), goja: tr("none","無") },
    { k: tr("Spec coverage","仕様準拠"), jsc: "ES2023", qjs: "ES2023", goja: "ES2023 eff." },
    { k: tr("Best for","最適用途"), jsc: tr("throughput · HTTP","スループット · HTTP"), qjs: tr("portability · scratch img","ポータビリティ · scratch image"), goja: tr("drop-in · pure Go","ドロップイン · 純Go") },
  ];
  const colHead = (name, tag, hi) => (
    <th style={{
      padding: "14px 16px", textAlign: "left",
      borderBottom: `1px solid ${hi ? RAMUNE_BLUE : "rgba(10,22,40,.08)"}`,
      background: hi ? "#f0f6ff" : "transparent",
      borderRadius: hi ? "8px 8px 0 0" : 0,
    }}>
      <div style={{ fontSize: 14, fontWeight: 600, color: RAMUNE_INK }}>{name}</div>
      <div style={{ fontSize: 11, fontFamily: "JetBrains Mono, monospace", color: hi ? RAMUNE_BLUE : "rgba(10,22,40,.5)", marginTop: 3 }}>{tag}</div>
    </th>
  );
  return (
    <div style={{ border: "1px solid rgba(10,22,40,.08)", borderRadius: 12, overflow: "hidden", background: "#fff" }}>
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13.5 }}>
        <thead>
          <tr>
            <th style={{ padding: "14px 16px", textAlign: "left", borderBottom: "1px solid rgba(10,22,40,.08)", fontSize: 12, fontWeight: 500, color: "rgba(10,22,40,.5)" }}>
              {tr("Backend","バックエンド")}
            </th>
            {colHead("JSC", "default", true)}
            {colHead("qjswasm", "-tags qjswasm")}
            {colHead("goja", "-tags goja")}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.k} style={{ borderBottom: i === rows.length - 1 ? "none" : "1px solid rgba(10,22,40,.05)" }}>
              <td style={{ padding: "12px 16px", fontWeight: 500, color: "rgba(10,22,40,.75)" }}>{r.k}</td>
              <td style={{ padding: "12px 16px", background: "#f8fbff", color: RAMUNE_INK, fontWeight: 500 }}>{r.jsc}</td>
              <td style={{ padding: "12px 16px", color: "rgba(10,22,40,.8)" }}>{r.qjs}</td>
              <td style={{ padding: "12px 16px", color: "rgba(10,22,40,.8)" }}>{r.goja}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function QuickstartSection() {
  const cliCode = `${sy.c("# easiest: prebuilt binaries via npm, no Go toolchain required")}
$ ${sy.f("npm")} install -g @ramunejs/cli

${sy.c("# or via Go, for JSC+JIT on Linux or custom build tags")}
$ ${sy.f("go install")} github.com/i2y/ramune/cmd/ramune@latest
$ ${sy.f("go install")} github.com/i2y/ramune/cmd/ramune-toolchain@latest
$ ${sy.f("ramune")} setup-jit   ${sy.c("# macOS: enable JIT (~10× faster)")}

${sy.c("# stronger isolation: build with -tags qjswasm → JS runs")}
${sy.c("# inside a wazero WASM VM (no host syscalls by default)")}
$ ${sy.f("go install")} ${sy.k("-tags")} qjswasm github.com/i2y/ramune/cmd/ramune@latest

${sy.c("# run a .ts file")}
$ ${sy.f("ramune")} run hello.ts

${sy.c("# untrusted code: language-level deny-all + allowlists")}
$ ${sy.f("ramune")} run ${sy.k("--sandbox")} \\
    ${sy.k("--allow-net")} example.com:443 \\
    ${sy.k("--allow-read")} ./data \\
    untrusted.ts

${sy.c("# commit the policy next to your handler")}
$ ${sy.f("cat")} ramune.toml
[${sy.t("permissions")}]
net        = ${sy.s('"allow"')}
net_hosts  = [${sy.s('"example.com:443"')}]
read       = ${sy.s('"allow"')}
read_paths = [${sy.s('"./data"')}]

${sy.c("# optional extra layer: Docker")}
$ ${sy.f("ramune")} run ${sy.k("--docker")} ${sy.k("--docker-memory")} 256 untrusted.ts`;

  const goCode = `${sy.c("// same knobs, from the Go embedding API")}
rt, _ := ramune.${sy.f("New")}(
  ramune.${sy.f("WithPermissions")}(&ramune.Permissions{
    Read:      ramune.PermGranted,
    ReadPaths: []${sy.t("string")}{${sy.s('"./data"')}},
    Net:       ramune.PermGranted,
    NetHosts:  []${sy.t("string")}{${sy.s('"example.com:443"')}},
    Write:     ramune.PermDenied,
    Env:       ramune.PermDenied,
    Run:       ramune.PermDenied,
  }),
  ramune.${sy.f("WithResourceLimits")}(ramune.ResourceLimits{
    MaxMemoryBytes: 64 &lt;&lt; 20,  ${sy.c("// 64 MiB")}
    MaxStackBytes:  1 &lt;&lt; 20,   ${sy.c("//  1 MiB")}
  }),
)
${sy.k("defer")} rt.${sy.f("Close")}()
rt.${sy.f("Eval")}(userScript)

${sy.c("// build with -tags qjswasm to run JS inside a wazero WASM VM")}
${sy.c("// (no host syscalls by default, stacks on top of the above)")}

${sy.c("// optional extra layer: Docker containment")}
ramune.${sy.f("SandboxRun")}(${sy.s('"untrusted.ts"')}, &ramune.SandboxConfig{
  Image: ${sy.s('"ubuntu:24.04"')}, MemoryMB: 256, NoNetwork: ${sy.k("true")},
})`;

  return (
    <section id="quickstart" style={{ padding: "40px 56px 40px", maxWidth: 1280, margin: "0 auto", scrollMarginTop: 80 }}>
      <SectionEyebrow num="I">{tr("Quickstart", "クイックスタート")}</SectionEyebrow>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 48, alignItems: "start" }}>
        <div>
          <h2 style={{
            fontFamily: "Inter Tight", fontSize: 40, letterSpacing: "-.025em",
            fontWeight: 600, margin: "10px 0 12px",
          }}>{tr(
            <>Try it from the CLI<br/>in one line.</>,
            <>CLI でまず動かす、<br/>1 行から。</>
          )}</h2>
          <p style={{ fontSize: 15.5, color: "rgba(10,22,40,.65)", lineHeight: 1.6, marginBottom: 22, maxWidth: 520 }}>
            {tr(
              "One go install and you have run · test · repl · check · fmt · lint · compile · serve. Sandboxing is layered — language-level deny-all / allow-lists on every backend, plus WASM VM isolation via wazero when you build with -tags qjswasm (JS has no host syscalls by default). Same knobs from CLI flags, ramune.toml, or Go options.",
              "go install 一度で run · test · repl · check · fmt · lint · compile · serve が揃う。サンドボックスは多層構造 — どのバックエンドでも言語層の deny-all / allow-list が効き、-tags qjswasm でビルドすれば wazero の WASM VM 内に JS が隔離される (host syscall は既定で無い)。CLI フラグ・ramune.toml・Go Option いずれも同じ設定項目。"
            )}
          </p>
          <div style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 520 }}>
            {[
              { k: "-tags qjswasm", v: tr("Build-time: JS runs inside a wazero WASM VM — no host syscalls by default. Complements (doesn't replace) --sandbox.","ビルド時: JS は wazero の WASM VM 内で動作、host syscall は既定で無い。--sandbox とは補完関係 (置き換えではない)。") },
              { k: "--sandbox", v: tr("Runtime: deny read / write / net / env / subprocess by default.","実行時: read / write / net / env / subprocess を既定で deny。") },
              { k: "--allow-{read,write,net,env,run}", v: tr("Comma-separated allowlists; empty value grants all.","カンマ区切りの allowlist (空指定で全許可)。") },
              { k: "ramune.toml", v: tr("[permissions] — commit the policy next to your handler.","[permissions] セクションにハンドラと並べてコミット。") },
              { k: tr("Go options","Go Option"), v: tr("WithPermissions · WithResourceLimits · NewSandboxRuntime — identical knobs from Go.","WithPermissions / WithResourceLimits / NewSandboxRuntime で Go からも同じ項目を設定。") },
              { k: "--docker · --docker-memory", v: tr("Optional extra layer: wrap the script in a Docker container.","任意の追加層: スクリプトを Docker コンテナで包む。") },
            ].map((r) => (
              <div key={r.k} style={{
                display: "grid", gridTemplateColumns: "210px 1fr", gap: 14,
                padding: "10px 0", borderBottom: "1px dashed rgba(10,22,40,.08)",
                fontSize: 13.5,
              }}>
                <div style={{ color: "rgba(10,22,40,.5)", fontFamily: "JetBrains Mono, monospace", fontSize: 11.5 }}>{r.k}</div>
                <div style={{ color: RAMUNE_INK }}>{r.v}</div>
              </div>
            ))}
          </div>
        </div>
        <CodeBlock
          tabs={[
            { name: "CLI", code: cliCode },
            { name: "Go", code: goCode },
          ]}
        />
      </div>
    </section>
  );
}

function HybridSection() {
  const hybridCode = `${sy.c("// app.ts — no annotations, no new syntax")}
${sy.k("export")} ${sy.k("function")} ${sy.f("fib")}(n: ${sy.t("number")}): ${sy.t("number")} {
  ${sy.k("return")} n &lt; 2 ? n : ${sy.f("fib")}(n - 1) + ${sy.f("fib")}(n - 2);
}

${sy.c("// $ ramune compile app.ts --hybrid -o myapp")}
${sy.c("//   extracted  function fib")}
${sy.c("//   skipped    function bucketSign  [object-type]")}
${sy.c("//   1 extracted, 1 skipped")}`;

  const goCode = `${sy.c("// generated Go, linked into the same binary")}
${sy.k("package")} hybrid

${sy.k("func")} ${sy.f("Fib")}(n ${sy.t("float64")}) ${sy.t("float64")} {
  ${sy.k("if")} n &lt; 2 {
    ${sy.k("return")} n
  }
  ${sy.k("return")} ${sy.f("Fib")}(n-1) + ${sy.f("Fib")}(n-2)
}

${sy.c("// fib(30) on qjswasm: 186 ms → 2 ms  (92×)")}
${sy.c("// fib(30) on JSC+JIT: 3.1 ms → 2.0 ms (1.55×)")}`;

  return (
    <section id="hybrid" style={{ padding: "40px 56px 60px", maxWidth: 1280, margin: "0 auto", scrollMarginTop: 80 }}>
      <SectionEyebrow num="III">{tr("Hybrid compilation", "ハイブリッドコンパイル")}</SectionEyebrow>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 48, alignItems: "start" }}>
        <div>
          <h2 style={{
            fontFamily: "Inter Tight", fontSize: 40, letterSpacing: "-.025em",
            fontWeight: 600, margin: "10px 0 12px",
          }}>
            {tr(
              <>Extract typed<br/>functions to Go.</>,
              <>型付き関数を<br/>Go に抽出。</>
            )}
          </h2>
          <p style={{ fontSize: 15.5, color: "rgba(10,22,40,.65)", lineHeight: 1.6, marginBottom: 22, maxWidth: 520 }}>
            {tr(
              "ramune compile --hybrid statically picks every function whose signature and body are provably semantically equivalent in Go, emits the Go source, and links it into the same binary. Rejected functions keep running on the JS floor — adding --hybrid never breaks correctness. Biggest wins on no-JIT pure-Go backends (qjswasm, goja).",
              "ramune compile --hybrid は、シグネチャと本体が Go と静的に等価と証明できる関数だけを抽出し、Go ソースを生成して同一バイナリにリンクします。抽出されなかった関数は JS フロアでそのまま動作 — --hybrid 追加で挙動が壊れることはありません。JIT のない純 Go バックエンド (qjswasm / goja) で最大の効果。"
            )}
          </p>
          <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
            {[
              { k: tr("Picker","ピッカー"), v: tr("Soundness-gated: signature + body AST must be statically equivalent to Go. No runtime profiling.","健全性ゲート: シグネチャと本体 AST が Go と静的に等価と証明できる関数のみ。実行時プロファイリングは使わない。") },
              { k: tr("Codegen","コード生成"), v: tr(".go files emitted alongside app.ts; go build links them into the compiled binary",".go ファイルを app.ts と並べて生成、go build でコンパイル済みバイナリにリンク") },
              { k: tr("Rejected","不採用関数"), v: tr("Keep running on the JS floor at the same speed as a JS-only build","JS フロアでそのまま実行、JS-only ビルドと同速度") },
              { k: tr("Biggest wins","最大の効果"), v: tr("qjswasm / goja (no JS JIT): typically 10×–350× on extractable kernels","qjswasm / goja (JIT 無し): 抽出可能カーネルで通常 10×–350×") },
            ].map((r) => (
              <div key={r.k} style={{
                display: "grid", gridTemplateColumns: "130px 1fr", gap: 14,
                padding: "10px 0", borderBottom: "1px dashed rgba(10,22,40,.08)",
                fontSize: 13.5,
              }}>
                <div style={{ color: "rgba(10,22,40,.5)", fontFamily: "JetBrains Mono, monospace", fontSize: 12 }}>{r.k}</div>
                <div style={{ color: RAMUNE_INK }}>{r.v}</div>
              </div>
            ))}
          </div>
          <div style={{
            marginTop: 22, display: "inline-flex", alignItems: "center", gap: 8,
            padding: "6px 12px", borderRadius: 999,
            background: `${RAMUNE_BLUE}12`, color: RAMUNE_BLUE,
            fontSize: 12, fontWeight: 600, fontFamily: "JetBrains Mono, monospace",
          }}>
            <Marble size={12}/>
            fib(30) on qjswasm: 186 ms → 2 ms · 92×
          </div>
        </div>
        <CodeBlock
          tabs={[
            { name: "app.ts", code: hybridCode },
            { name: "generated.go", code: goCode },
          ]}
        />
      </div>
    </section>
  );
}

function BenchChart({ title, bars, max }) {
  return (
    <div>
      <div style={{
        fontSize: 12, fontFamily: "JetBrains Mono, monospace",
        color: "rgba(10,22,40,.55)", marginBottom: 16,
      }}>{title}</div>
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        {bars.map((b) => (
          <div key={b.l} style={{ display: "grid", gridTemplateColumns: "160px 1fr 110px", alignItems: "center", gap: 12 }}>
            <div style={{ fontSize: 12.5, color: b.hi ? RAMUNE_INK : "rgba(10,22,40,.7)", fontWeight: b.hi ? 600 : 500, display: "flex", alignItems: "center", gap: 6 }}>
              {b.l}
              {b.badge && <span style={{
                fontSize: 9, padding: "1px 5px", borderRadius: 3,
                background: RAMUNE_BLUE, color: "#fff", fontWeight: 700, letterSpacing: ".04em",
              }}>{b.badge}</span>}
            </div>
            <div style={{ height: 22, background: "rgba(10,22,40,.04)", borderRadius: 4, position: "relative" }}>
              <div style={{
                position: "absolute", left: 0, top: 0, bottom: 0,
                width: `${Math.max(1, (b.v / max) * 100)}%`,
                background: b.hi ? `linear-gradient(90deg, ${RAMUNE_BLUE}, ${RAMUNE_AQUA})` : "rgba(10,22,40,.2)",
                borderRadius: 4,
                transition: "width .6s",
              }}/>
            </div>
            <div style={{ fontSize: 12, fontFamily: "JetBrains Mono, monospace", color: b.hi ? RAMUNE_BLUE : "rgba(10,22,40,.6)", textAlign: "right", fontWeight: b.hi ? 600 : 400 }}>{b.label}</div>
          </div>
        ))}
      </div>
    </div>
  );
}

export { VariantB };
