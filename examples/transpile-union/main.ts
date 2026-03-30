// Union types example: nullable, string literal unions, discriminated unions

// --- Nullable types (T | null → *T) ---

function greet(name: string | null): string {
  if (name === null) {
    return "Hello, stranger!";
  }
  return "Hello, " + name + "!";
}

console.log(greet("Alice"));
console.log(greet(null));

// --- String literal unions → string ---

type Direction = "north" | "south" | "east" | "west";

function walk(dir: Direction): string {
  return "Walking " + dir;
}

console.log(walk("north"));
console.log(walk("east"));

// --- Discriminated unions → Go interface + type assertions ---

interface Circle {
  kind: "circle";
  radius: number;
}

interface Rectangle {
  kind: "rectangle";
  width: number;
  height: number;
}

type Shape = Circle | Rectangle;

function area(shape: Shape): number {
  if (shape.kind === "circle") {
    return Math.PI * shape.radius * shape.radius;
  } else if (shape.kind === "rectangle") {
    return shape.width * shape.height;
  }
  return 0;
}

function describe(shape: Shape): string {
  if (shape.kind === "circle") {
    return "Circle (r=" + shape.radius + ")";
  } else if (shape.kind === "rectangle") {
    return "Rect (" + shape.width + "x" + shape.height + ")";
  }
  return "Unknown";
}

// Create shapes with explicit types
const c: Circle = { kind: "circle", radius: 5 };
const r: Rectangle = { kind: "rectangle", width: 4, height: 6 };

console.log("\nShapes:");
console.log("  " + describe(c) + " → area=" + area(c));
console.log("  " + describe(r) + " → area=" + area(r));
