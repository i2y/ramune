# Ramune TypeScript-to-Go Transpiler

Back to [`README.md`](./README.md).

> **Warning:** This feature is experimental and under active development. While many TypeScript patterns are supported, complex real-world codebases (e.g., frameworks with advanced generics, method overloads, or nullable string patterns) may produce code that requires manual fixes. Contributions and bug reports are welcome.

Ramune includes a built-in TypeScript-to-Go transpiler. It converts TypeScript source code to idiomatic Go, supporting a wide range of TypeScript features.

## Supported TypeScript Features

| Category | Features |
|----------|----------|
| **Types** | `number` → `float64`, `string`, `boolean`, `void`, `null`/`undefined` → `nil`, `any`, `unknown` → `any` |
| **Collections** | `T[]` → `[]T`, `Map<K,V>` → `map[K]V`, `Set<T>` → `map[T]struct{}` |
| **Nullable** | `T \| null`, `T \| undefined` → `*T` (pointer) |
| **Generics** | Functions and classes with type parameters and constraints |
| **Enums** | String enums → Go const block, numeric enums → iota |
| **Discriminated unions** | `type Shape = Circle \| Square` with narrowing via `if (s.kind === "circle")` |
| **Union of literals** | `"a" \| "b" \| "c"` → `string` |
| **Promises** | `Promise<T>` → `*promise.Promise[T]`, `async`/`await` supported |
| **Classes** | Fields, constructors, methods, inheritance (`extends`), static fields/methods, abstract classes, getter/setter accessors |
| **Interfaces** | Converted to Go struct types |
| **Destructuring** | Object `{a, b}` and array `[x, y]` patterns, default values (`{a = 1}`) |
| **Spread** | `[...arr, elem]`, `{...obj, key: val}` |
| **Template literals** | `` `Hello ${name}` `` → `fmt.Sprintf` |
| **Operators** | `typeof`, `instanceof`, `in`, `delete`, `?.` (optional chaining), `??` (nullish coalescing), `**` (exponentiation), `>>>`, `&&=`, `\|\|=`, `??=` |
| **Array methods** | `map`, `filter`, `reduce`, `find`, `forEach`, `includes`, `some`, `every`, `push`, `pop`, `slice`, `splice`, `concat`, `join`, etc. |
| **String methods** | `split`, `includes`, `startsWith`, `endsWith`, `trim`, `replace`, `replaceAll`, `repeat`, `padStart`, `padEnd`, etc. |
| **Loops** | `for`, `for...of`, `for...in`, `for await...of`, `while`, `do...while`, labeled `break`/`continue` |
| **Error handling** | `try`/`catch`/`finally` |
| **Modules** | Relative imports, Node.js built-in modules, npm packages (uuid, lodash, zod), Go packages via `go:` prefix, `export default`, re-exports (`export { x } from './mod'`) |
| **Go interop** | `go:` prefix imports any Go package, multi-return destructuring (`const [resp, err] = http.Get(url)`), auto go.mod for third-party modules |
| **Type resolution** | Conditional types, mapped types (`Record<K,V>` → `map[K]V`), intersection types, utility types via checker |

## Type Mapping

| TypeScript | Go |
|---|---|
| `number` | `float64` |
| `string` | `string` |
| `boolean` | `bool` |
| `T[]` | `[]T` |
| `T \| null` | `*T` |
| `Map<K, V>` | `map[K]V` |
| `Set<T>` | `map[T]struct{}` |
| `Promise<T>` | `*promise.Promise[T]` |
| `any` / `unknown` | `any` |
| Enum | Named type with const block |
| Class | Struct with methods |
| Interface | Struct type |

## Native Extension Module Workflow

The `--native` flag in `ramune compile` transpiles TypeScript to Go and exposes exported functions as `require()`-able JavaScript modules:

```
TypeScript → Go transpiler → Go source → NativeModuleFromFuncs → require('native:name')
```

**Supported native function signatures:**
- Primitive parameters and returns: `number`, `string`, `boolean`
- Struct parameters: JS objects auto-converted to Go structs via field name matching
- Struct returns: Go structs returned as JS objects with live getter/setter properties
- Typed slice parameters: `number[]` → `[]float64`, etc.
- Typed slice/map returns: `[]int` → JS array, `map[string]int` → JS object, nested supported
- Error returns: `(T, error)` - errors become JS exceptions
- Pointer returns: `*Struct` - returned with methods and mutable fields
- Panics caught and converted to JS exceptions

**Not supported as native exports:**
- Generic functions (must be wrapped in a non-generic function)
- Functions with channel parameters
- Functions with interface{} parameters other than `any`

## Go Package Imports (`go:` prefix)

Import any Go standard library or third-party package directly in TypeScript:

```typescript
import { Println, Sprintf } from "go:fmt"
import * as http from "go:net/http"
import { Default } from "go:github.com/gin-gonic/gin"

const [resp, err] = http.Get("http://example.com")  // Go multi-return
Println(Sprintf("status: %d", resp.StatusCode))
```

Generate TypeScript type definitions for Go packages to enable type checking:

```bash
ramune typegen go:fmt go:net/http -o go.d.ts
```

This generates `declare module "go:fmt" { ... }` with full function signatures, interfaces, and type mappings.

## Transpiler Limitations

- **No decorators** - complex metaprogramming, not yet supported
- **No generators / `yield`** - Go has no generator concept; use channels or callbacks instead
- **No dynamic `import()`** - use `require()` (which is supported)
- **No Symbol or Proxy** - no Go equivalent
- **Generic function exports** - cannot be exposed as native module exports; wrap in a non-generic function
- **Numeric precision** - all numbers are `float64` (no BigInt-like arbitrary precision)
- **`any`-typed property access** - uses `jsrt.GetField()` runtime reflection (struct fields and map keys)
