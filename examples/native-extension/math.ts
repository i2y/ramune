// Native extension: performance-critical math functions
// These will be transpiled to Go and called from JS at native speed.

export function fibonacci(n: number): number {
  if (n <= 1) return n;
  let a: number = 0;
  let b: number = 1;
  for (let i = 2; i <= n; i++) {
    const temp = a + b;
    a = b;
    b = temp;
  }
  return b;
}

export function isPrime(n: number): boolean {
  if (n < 2) return false;
  for (let i = 2; i * i <= n; i++) {
    if (n % i === 0) return false;
  }
  return true;
}

export function factorial(n: number): number {
  let result: number = 1;
  for (let i = 2; i <= n; i++) {
    result *= i;
  }
  return result;
}
