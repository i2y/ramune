// lodash-mini — a representative subset of lodash's array/number/string
// helpers, hand-written in idiomatic TS to measure how the picker fares
// against real-world lodash-shaped code. Each function below is the
// kind of utility a project would copy from lodash into a local helper
// when they don't want the full dep — typed, no `any`, no fancy
// metaprogramming. The picker report (run.sh) tells us which lower
// cleanly to native Go and which need the JS floor.

// --- Numeric ---

export function sum(xs: number[]): number {
  let total = 0;
  for (let i = 0; i < xs.length; i++) total = total + xs[i];
  return total;
}

export function mean(xs: number[]): number {
  if (xs.length === 0) return 0;
  return sum(xs) / xs.length;
}

export function clamp(v: number, lo: number, hi: number): number {
  if (v < lo) return lo;
  if (v > hi) return hi;
  return v;
}

export function inRange(v: number, lo: number, hi: number): boolean {
  return v >= lo && v < hi;
}

// `findIndex` exercise — needs JSFunc-bridge form (callback param).
export function firstIndexMatching(xs: number[], pred: (n: number) => boolean): number {
  return xs.findIndex(pred);
}

// --- Array ---

export function chunk(xs: number[], size: number): number[][] {
  const out: number[][] = [];
  if (size <= 0) return out;
  for (let i = 0; i < xs.length; i = i + size) {
    const piece: number[] = [];
    for (let j = i; j < i + size && j < xs.length; j++) piece.push(xs[j]);
    out.push(piece);
  }
  return out;
}

export function take(xs: number[], n: number): number[] {
  const out: number[] = [];
  for (let i = 0; i < n && i < xs.length; i++) out.push(xs[i]);
  return out;
}

export function drop(xs: number[], n: number): number[] {
  const out: number[] = [];
  for (let i = n; i < xs.length; i++) out.push(xs[i]);
  return out;
}

export function uniq(xs: number[]): number[] {
  const out: number[] = [];
  for (let i = 0; i < xs.length; i++) {
    let seen = false;
    for (let j = 0; j < out.length; j++) {
      if (out[j] === xs[i]) { seen = true; break; }
    }
    if (!seen) out.push(xs[i]);
  }
  return out;
}

export function reverse(xs: number[]): number[] {
  const out: number[] = [];
  for (let i = xs.length - 1; i >= 0; i--) out.push(xs[i]);
  return out;
}

// --- String ---

export function repeat(s: string, n: number): string {
  let out = "";
  for (let i = 0; i < n; i++) out = out + s;
  return out;
}

export function padStart(s: string, len: number, pad: string): string {
  if (s.length >= len) return s;
  return repeat(pad, len - s.length) + s;
}

export function startsWithAny(s: string, prefixes: string[]): boolean {
  for (let i = 0; i < prefixes.length; i++) {
    if (s.startsWith(prefixes[i])) return true;
  }
  return false;
}

// --- Misc ---

export function toFloat(s: string): number {
  return parseFloat(s);
}

export function isPalindrome(s: string): boolean {
  const n = s.length;
  for (let i = 0; i < n / 2; i++) {
    if (s.charAt(i) !== s.charAt(n - 1 - i)) return false;
  }
  return true;
}
