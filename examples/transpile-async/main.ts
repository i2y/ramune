// Async/await example

async function fetchGreeting(name: string): Promise<string> {
  return `Hello, ${name}!`;
}

async function fetchNumber(): Promise<number> {
  return 42;
}

// Top-level await
const greeting = await fetchGreeting("World");
console.log(greeting);

const num = await fetchNumber();
console.log("Number:", num);

console.log("Done!");
