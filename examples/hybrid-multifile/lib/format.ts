// String formatting kernel in its own file. The picker accepts primitive
// args/return, string concatenation, and the string / array method safelist.

export function fmtPoint(x: number, y: number): string {
  return "(" + x + ", " + y + ")";
}

export function repeat(s: string, n: number): string {
  return s.repeat(n);
}
