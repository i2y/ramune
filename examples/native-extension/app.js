// JS application that uses native Go extensions for heavy computation
const { fibonacci, isPrime, factorial } = require('native:math');

console.log("=== Native Extension Demo ===");
console.log("These functions run as compiled Go, not interpreted JS!\n");

// Fibonacci
console.log("Fibonacci sequence:");
for (let i = 1; i <= 10; i++) {
  console.log(`  fib(${i}) = ${fibonacci(i)}`);
}

// Prime numbers
console.log("\nPrimes up to 50:");
const primes = [];
for (let n = 2; n <= 50; n++) {
  if (isPrime(n)) primes.push(n);
}
console.log(" ", primes.join(", "));

// Factorials
console.log("\nFactorials:");
for (let i = 1; i <= 10; i++) {
  console.log(`  ${i}! = ${factorial(i)}`);
}
