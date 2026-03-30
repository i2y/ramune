// Optional chaining, nullish coalescing, and exponentiation example

// --- Optional chaining + nullish coalescing ---

function getCity(user: any): any {
  return user?.address?.city ?? "unknown"
}

function getName(user: any): any {
  return user?.name ?? "anonymous"
}

const alice: any = { name: "Alice", address: { city: "Tokyo" } };

console.log("Optional chaining + nullish coalescing:");
console.log("  Alice city:", getCity(alice));
console.log("  Alice name:", getName(alice));
console.log("  null name:", getName(null));

// --- Nullish coalescing ---

function withDefault(value: any, fallback: string): any {
  return value ?? fallback
}

console.log("\nNullish coalescing:");
console.log("  'hello' ?? 'default':", withDefault("hello", "default"));
console.log("  null ?? 'default':", withDefault(null, "default"));

// --- Exponentiation ---

function square(x: number): number {
  return x ** 2
}

function cube(x: number): number {
  return x ** 3
}

function hypotenuse(a: number, b: number): number {
  return (a ** 2 + b ** 2) ** 0.5
}

console.log("\nExponentiation:");
console.log("  3 ** 2 =", square(3));
console.log("  2 ** 3 =", cube(2));
console.log("  hypotenuse(3, 4) =", hypotenuse(3, 4));
