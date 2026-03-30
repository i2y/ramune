// Practical lodash usage example
// Demonstrates data processing patterns commonly used in real applications

import {
  chunk, compact, uniq, difference, intersection,
  groupBy, sortBy, range, flatten,
  pick, omit, isEmpty,
  capitalize, camelCase, snakeCase, kebabCase, truncate,
  clamp, debounce, throttle
} from 'lodash';

// --- Data Processing ---

interface User {
  name: string;
  age: number;
  department: string;
  active: boolean;
}

const users: User[] = [
  { name: "Alice", age: 30, department: "Engineering", active: true },
  { name: "Bob", age: 25, department: "Marketing", active: false },
  { name: "Charlie", age: 35, department: "Engineering", active: true },
  { name: "Diana", age: 28, department: "Marketing", active: true },
  { name: "Eve", age: 32, department: "Engineering", active: true },
  { name: "Frank", age: 45, department: "Sales", active: false },
];

// Group users by department
console.log("=== Group By Department ===");
const byDept = groupBy(users, (u: User) => u.department);
console.log("Engineering:", byDept["Engineering"]);
console.log("Marketing:", byDept["Marketing"]);

// Sort by age
console.log("\n=== Sorted by Age ===");
const sorted = sortBy(users, (u: User) => u.age);
for (let i = 0; i < sorted.length; i++) {
  console.log(`  ${sorted[i].name}: ${sorted[i].age}`);
}

// --- Array Operations ---

console.log("\n=== Array Operations ===");

const scores: number[] = [85, 92, 0, 78, 0, 95, 88, 0, 72];
console.log("Original:", scores);
console.log("Compact (remove zeros):", compact(scores));
console.log("Unique:", uniq([1, 2, 2, 3, 3, 3, 4]));

const teamA: number[] = [1, 2, 3, 4, 5];
const teamB: number[] = [3, 4, 5, 6, 7];
console.log("Team A:", teamA);
console.log("Team B:", teamB);
console.log("Only in A:", difference(teamA, teamB));
console.log("In both:", intersection(teamA, teamB));

const nested: number[][] = [[1, 2], [3, 4], [5, 6]];
console.log("Flatten:", flatten(nested));

const batches = chunk(range(1, 11), 3);
console.log("Batches of 3:", batches);

// --- Object Operations ---

console.log("\n=== Object Operations ===");

const config = { host: "localhost", port: 8080, debug: true, secret: "s3cr3t", timeout: 30 };
console.log("Full config:", config);
console.log("Pick host+port:", pick(config, "host", "port"));
console.log("Omit secret:", omit(config, "secret"));
console.log("Is empty?:", isEmpty(config));
console.log("Empty obj?:", isEmpty({}));

// --- String Operations ---

console.log("\n=== String Operations ===");
console.log("capitalize:", capitalize("hello world"));
console.log("camelCase:", camelCase("hello-world-foo"));
console.log("snakeCase:", snakeCase("helloWorldFoo"));
console.log("kebabCase:", kebabCase("helloWorldFoo"));
console.log("truncate:", truncate("This is a very long string that should be truncated", 30));

// --- Math ---

console.log("\n=== Math ===");
console.log("clamp(-5, 0, 10):", clamp(-5, 0, 10));
console.log("clamp(15, 0, 10):", clamp(15, 0, 10));
console.log("clamp(5, 0, 10):", clamp(5, 0, 10));
console.log("range(0, 5):", range(0, 5));
console.log("range(0, 20, 5):", range(0, 20, 5));
