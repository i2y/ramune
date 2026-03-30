// Native struct round-trip: Go structs ↔ JS objects
const { makePoint, distance, midpoint } = require('native:types');

const p1 = makePoint(0, 0);
const p2 = makePoint(3, 4);

console.log("p1:", JSON.stringify(p1));
console.log("p2:", JSON.stringify(p2));
console.log("distance:", distance(p1, p2));

const mid = midpoint(p1, p2);
console.log("midpoint:", JSON.stringify(mid));
