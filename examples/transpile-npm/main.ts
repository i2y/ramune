// npm package adapter example
// Uses uuid and lodash via Go adapters (no node_modules required)

import { v4 as uuidv4 } from 'uuid';
import { chunk, uniq, capitalize, range, difference } from 'lodash';

// Generate UUIDs
const id1 = uuidv4();
const id2 = uuidv4();
console.log("UUID 1:", id1);
console.log("UUID 2:", id2);

// Lodash array operations
const data: number[] = [3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5];
console.log("\nOriginal:", data);

const unique = uniq(data);
console.log("Unique:", unique);

const chunks = chunk(data, 3);
console.log("Chunks of 3:", chunks);

// Set operations
const a: number[] = [1, 2, 3, 4, 5];
const b: number[] = [3, 4, 5, 6, 7];
console.log("\nDifference a-b:", difference(a, b));

// String operations
const words: string[] = ["hello", "world", "foo"];
const capitalized = words.map((w: string) => capitalize(w));
console.log("Capitalized:", capitalized);

// Range
const r = range(1, 6);
console.log("\nRange 1-5:", r);
