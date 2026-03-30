const { newCounter } = require('native:counter');

const c = newCounter("hits");
console.log("Initial count:", c.count);

c.increment();
console.log("After increment:", c.count);

c.increment();
c.increment();
console.log("After 2 more:", c.count);

c.decrement();
console.log("After decrement:", c.count);

console.log("Name:", c.name);
console.log("Describe:", c.describe());

// Test setter
c.count = 100;
console.log("After set to 100:", c.count);
c.increment();
console.log("After increment:", c.count);
