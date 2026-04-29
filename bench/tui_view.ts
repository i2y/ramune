// TUI view() benchmark — three workloads designed to extract through
// the hybrid AOT picker. Each is run a fixed number of iterations to
// keep latency comparisons honest across the JSC and hybrid binaries.
//
//   ramune compile --hybrid -o tui_view_hybrid bench/tui_view.ts
//   ramune compile          -o tui_view_jsc    bench/tui_view.ts
//   ./tui_view_hybrid; ./tui_view_jsc
//
// The runtime is the same; only whether view*/* extracts to Go differs.

interface Counter {
  count: number;
  paused: boolean;
}

const RED = "\x1b[31m";
const GREEN = "\x1b[32m";
const CYAN = "\x1b[36m";
const BOLD = "\x1b[1m";
const DIM = "\x1b[2m";
const RESET = "\x1b[0m";

// Tiny — primitive args only. Closest to a "pure call cost" probe.
export function viewTiny(n: number, label: string): string {
  return label + ": " + n;
}

// Interface — struct arg + branchy formatting. Mid-weight.
export function viewCounter(model: Counter): string {
  const sign = model.count >= 0 ? "+" : "";
  const color = model.paused ? DIM : model.count > 0 ? GREEN : model.count < 0 ? RED : CYAN;
  const status = model.paused ? "paused" : "running";
  return BOLD + "Counter" + RESET + "\n" +
    color + sign + model.count + RESET + "\n" +
    DIM + status + RESET;
}

// Heavy — string-building loop. Reflects a progress / spark-line render.
export function progressBar(value: number, max: number, width: number): string {
  if (max <= 0) return "";
  const ratio = value / max;
  const filled = Math.round(ratio * width);
  let bar = "";
  for (let i = 0; i < width; i++) {
    bar = bar + (i < filled ? "█" : "░");
  }
  return bar + " " + Math.round(ratio * 100) + "%";
}

// Wide table render — lots of arithmetic + string concat per call.
// Used to find the cross-over where the AOT body work outweighs the
// FFI bridge cost. Each call walks `rows × cols` cells.
export function gridRender(seed: number, rows: number, cols: number): string {
  let out = "";
  for (let r = 0; r < rows; r++) {
    let line = "";
    for (let c = 0; c < cols; c++) {
      const v = (seed + r * 97 + c * 13) % 256;
      line = line + (v < 64 ? "·" : v < 128 ? "▪" : v < 192 ? "▫" : "■");
    }
    out = out + line + "\n";
  }
  return out;
}

// observe is the global sink used to defeat dead-code elimination —
// every benchmark adds the rendered string's first char-code to it,
// then prints the total. That's enough side effect to force JSC's JIT
// to actually call the view per iteration; without this guard a tiny
// view inlines + folds away to almost zero ns/op which makes the AOT
// comparison meaningless.
let observe = 0;

function bench(label: string, n: number, fn: (i: number) => string): void {
  // Warm-up — let JSC's JIT settle.
  for (let i = 0; i < 1000; i++) {
    const s = fn(i);
    observe = (observe + s.charCodeAt(0)) | 0;
  }
  const t0 = Date.now();
  for (let i = 0; i < n; i++) {
    const s = fn(i);
    observe = (observe + s.charCodeAt(0)) | 0;
  }
  const elapsed = Date.now() - t0;
  const nsPerOp = (elapsed * 1_000_000) / Math.max(1, n);
  const opsPerSec = (n * 1000) / Math.max(1, elapsed);
  console.log(
    label.padEnd(16) +
    " " + (elapsed + "ms").padStart(7) +
    "  " + nsPerOp.toFixed(0).padStart(8) + " ns/op" +
    "  " + opsPerSec.toFixed(0).padStart(10) + " op/s"
  );
}

const N_TINY = 200_000;
const N_COUNTER = 50_000;
const N_PROGRESS = 50_000;

const sample: Counter = { count: 0, paused: false };

console.log("workload          elapsed     ns/op           op/s");
console.log("------------------------------------------------------");

bench("viewTiny", N_TINY, (i) => viewTiny(i & 0xff, "x"));

bench("viewCounter", N_COUNTER, (i) => {
  sample.count = i & 0x7f;
  sample.paused = (i & 1) === 0;
  return viewCounter(sample);
});

bench("progressBar", N_PROGRESS, (i) => progressBar(i % 100, 100, 40));

// Heavy: wide grid render. Runtime per call grows with rows×cols, so
// the cross-over with FFI bridge cost shows up as size scales.
bench("grid 4x40", 20_000, (i) => gridRender(i, 4, 40));
bench("grid 16x80", 5_000, (i) => gridRender(i, 16, 80));
bench("grid 32x120", 1_000, (i) => gridRender(i, 32, 120));

console.log("observe sink:", observe);
