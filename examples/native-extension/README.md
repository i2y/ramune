# Native Extension Example

Demonstrates how to transpile TypeScript functions to Go and call them
from JavaScript at native speed via `require('native:...')`.

## Files

- `math.ts` — Performance-critical functions (fibonacci, isPrime, factorial)
- `app.js` — JS application that uses the native functions

## Build & Run

```bash
ramune compile --native math.ts app.js -o demo
./demo
```

## How It Works

1. `math.ts` is transpiled to Go by the TS→Go transpiler
2. The transpiled Go functions are registered as a JS module `native:math`
3. `app.js` calls `require('native:math')` to access the functions
4. The functions execute as compiled Go code — no JS interpretation overhead
