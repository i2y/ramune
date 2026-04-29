// Pure-string TUI view functions designed to lower through the hybrid
// AOT picker. The discipline:
//
//   1. Inputs are primitives or named interfaces with primitive fields.
//   2. Output is a string.
//   3. Body composes strings via `+` / template literals — no calls
//      into Ramune.tui.style etc., because those are runtime-only
//      property accesses the picker can't extract.
//   4. ANSI escapes are baked in as constants where styling matters.
//
// Run the picker to see what gets extracted:
//
//   ../../ramune compile --hybrid --hybrid-report -o /tmp/tui_view.bin view.ts
//
// Functions that pass the gate become native Go in the emitted binary;
// the rest stay on the JS floor.

interface Counter {
  count: number;
  paused: boolean;
}

interface Pomodoro {
  remainingSec: number;
  cycles: number;
  phase: "work" | "break";
}

// ANSI helpers — string constants the picker treats as opaque text.
const RED = "\x1b[31m";
const GREEN = "\x1b[32m";
const CYAN = "\x1b[36m";
const BOLD = "\x1b[1m";
const DIM = "\x1b[2m";
const RESET = "\x1b[0m";

export function pad2(n: number): string {
  if (n < 10) return "0" + n;
  return "" + n;
}

export function viewCounter(model: Counter): string {
  const sign = model.count >= 0 ? "+" : "";
  const color = model.paused ? DIM : model.count > 0 ? GREEN : model.count < 0 ? RED : CYAN;
  const status = model.paused ? "paused" : "running";
  return BOLD + "Counter" + RESET + "\n" +
    color + sign + model.count + RESET + "\n" +
    DIM + status + RESET;
}

export function viewPomodoro(model: Pomodoro): string {
  const min = Math.floor(model.remainingSec / 60);
  const sec = model.remainingSec % 60;
  const phaseColor = model.phase === "work" ? RED : GREEN;
  const phaseLabel = model.phase === "work" ? "FOCUS" : "BREAK";
  const time = pad2(min) + ":" + pad2(sec);
  return phaseColor + BOLD + phaseLabel + RESET + "\n" +
    BOLD + time + RESET + "\n" +
    DIM + "cycles: " + model.cycles + RESET;
}

// Plain string formatter — the simplest possible extractable view.
export function viewSimple(value: number, label: string): string {
  return label + ": " + value;
}

// Helper that lifts to native Go with no JS bridge per call. Useful for
// per-frame work like generating a progress bar.
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
