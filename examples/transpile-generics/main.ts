// Generics example

function identity<T>(value: T): T {
  return value;
}

function first<T>(arr: T[]): T {
  return arr[0];
}

function last<T>(arr: T[]): T {
  return arr[arr.length - 1];
}

function pair<A, B>(a: A, b: B): [A, B] {
  return [a, b];
}

// Generic with constraint
function longest<T extends string>(a: T, b: T): T {
  if (a.length >= b.length) {
    return a;
  }
  return b;
}

// Usage
console.log(identity<number>(42));
console.log(identity<string>("hello"));

const nums: number[] = [10, 20, 30, 40, 50];
console.log("First:", first(nums));
console.log("Last:", last(nums));

const names: string[] = ["Alice", "Bob", "Charlie"];
console.log("First name:", first(names));
console.log("Last name:", last(names));

console.log("Longest:", longest("hello", "hi"));
console.log("Longest:", longest("go", "typescript"));
