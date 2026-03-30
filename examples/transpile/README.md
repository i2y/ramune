# TypeScript-to-Go Transpile Example

This example demonstrates `ramune transpile` — converting TypeScript source files
into a native Go binary without requiring JavaScriptCore at runtime.

## Files

- `main.ts` — Entry point with imports, loops, arrays, string ops, interfaces
- `utils.ts` — Exported utility functions (greet, add, multiply)
- `math.ts` — Exported math functions (fibonacci, isPrime)

## Usage

### Transpile to Go source

```bash
ramune transpile -o out -module demo main.ts utils.ts math.ts
```

This generates:

```
out/
  go.mod
  main.go           # from main.ts
  utils/utils.go    # from utils.ts
  math/math.go      # from math.ts
```

### Transpile and compile to binary in one step

```bash
ramune transpile --compile -o demo main.ts utils.ts math.ts
./demo
```

### Output

```
Hello, Ramune!
10 + 32 = 42
6 * 7 = 42

Fibonacci:
  fib(1) = 1
  fib(2) = 1
  ...
  fib(10) = 55

Primes up to 30:
  [2 3 5 7 11 13 17 19 23 29]

Array stats:
  nums: [5 3 8 1 9 2 7 4 6]
  max: 9 min: 1 sum: 45

String ops:
  upper: HELLO, WORLD
  includes 'world': true
  split: [hello world]

Config: {localhost 8080 false}
```
