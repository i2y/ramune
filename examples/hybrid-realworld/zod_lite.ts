// zod-lite — a minimal validator library shaped after zod's API. Heavy
// on conditional checks, branded result types, and union-of-issues —
// the kind of code where the picker's union/object handling gets
// stress-tested. Validators take typed input and return a boolean
// "isValid" or a numeric error code; we deliberately avoid the full
// Result<T, Issue> sum-type that zod uses (which would route the whole
// thing through discriminated unions the picker doesn't accept yet).

// --- Number validators ---

export function isInt(n: number): boolean {
  return Number.isInteger(n);
}

export function isFinitePositive(n: number): boolean {
  return Number.isFinite(n) && n > 0;
}

export function inRangeInclusive(n: number, lo: number, hi: number): boolean {
  return n >= lo && n <= hi;
}

// --- String validators ---

export function nonEmpty(s: string): boolean {
  return s.length > 0;
}

export function lengthBetween(s: string, lo: number, hi: number): boolean {
  return s.length >= lo && s.length <= hi;
}

export function startsWith(s: string, prefix: string): boolean {
  return s.startsWith(prefix);
}

export function endsWith(s: string, suffix: string): boolean {
  return s.endsWith(suffix);
}

export function looksLikeEmail(s: string): boolean {
  // Cheap heuristic — full RFC 5322 needs regex, which the picker
  // rejects anyway. Good enough for a "real-world picker" probe.
  if (s.length < 3) return false;
  const at = s.indexOf("@");
  if (at <= 0 || at >= s.length - 1) return false;
  const dot = s.indexOf(".", at + 1);
  return dot > at && dot < s.length - 1;
}

// --- Array validators ---

export function allInts(xs: number[]): boolean {
  for (let i = 0; i < xs.length; i++) {
    if (!Number.isInteger(xs[i])) return false;
  }
  return true;
}

export function allUnique(xs: number[]): boolean {
  for (let i = 0; i < xs.length; i++) {
    for (let j = i + 1; j < xs.length; j++) {
      if (xs[i] === xs[j]) return false;
    }
  }
  return true;
}

// --- Composite ---

interface User { age: number; name: string; }

export function isAdult(u: User): boolean {
  return u.age >= 18 && u.name.length > 0;
}

interface Order {
  id: number;
  total: number;
  items: number;
}

export function isReasonable(o: Order): boolean {
  return o.id > 0 && o.total >= 0 && o.items > 0 && o.items < 1000;
}

// --- Discriminated kind (currently rejected: wide-union) ---

type Kind = "user" | "guest";

export function kindLabel(k: Kind): string {
  if (k === "user") return "registered";
  return "anonymous";
}

// --- Nullable input (picker now accepts) ---

export function nameOrAnon(name: string | null): string {
  return name ?? "anonymous";
}

export function safeAge(age: number | undefined): number {
  return age ?? 0;
}
