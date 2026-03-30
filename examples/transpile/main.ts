import { greet, add, multiply } from './utils';
import { fibonacci, isPrime } from './math';

// Basic function calls
const message = greet("Ramune");
console.log(message);
console.log("10 + 32 =", add(10, 32));
console.log("6 * 7 =", multiply(6, 7));

// Fibonacci sequence
console.log("\nFibonacci:");
for (let i = 1; i <= 10; i++) {
  console.log(`  fib(${i}) = ${fibonacci(i)}`);
}

// Prime check
console.log("\nPrime check:");
for (let n = 2; n <= 20; n++) {
  if (isPrime(n)) {
    console.log(`  ${n} is prime`);
  }
}

// Array operations
const nums: number[] = [5, 3, 8, 1, 9, 2, 7, 4, 6];
let max: number = nums[0];
let min: number = nums[0];
let sum: number = 0;
for (let i = 0; i < nums.length; i++) {
  if (nums[i] > max) max = nums[i];
  if (nums[i] < min) min = nums[i];
  sum += nums[i];
}
console.log("\nArray stats:");
console.log("  max:", max, "min:", min, "sum:", sum);

// String operations
const text = "hello, world";
console.log("\nString ops:");
console.log("  upper:", text.toUpperCase());
console.log("  includes 'world':", text.includes("world"));
console.log("  split:", text.split(", "));

// Type-safe config
interface Config {
  host: string;
  port: number;
  debug: boolean;
}

function createConfig(host: string, port: number): Config {
  return { host, port, debug: false };
}

const config = createConfig("localhost", 8080);
console.log("\nConfig:", config);
