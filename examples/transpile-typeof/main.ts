// typeof / instanceof / in / delete operators example

// --- typeof ---

function isString(x: any): boolean {
  return typeof x === "string";
}

function isNumber(x: any): boolean {
  return typeof x === "number";
}

function isNil(x: any): boolean {
  return typeof x === "undefined";
}

console.log("typeof tests:");
console.log("  isString('hello'):", isString("hello"));
console.log("  isString(42):", isString(42));
console.log("  isNumber(42):", isNumber(42));
console.log("  isNumber('hi'):", isNumber("hi"));
console.log("  isNil(null):", isNil(null));

// --- instanceof ---

class Dog {
  name: string;
  breed: string;
  constructor(name: string, breed: string) {
    this.name = name;
    this.breed = breed;
  }
}

class Cat {
  name: string;
  constructor(name: string) {
    this.name = name;
  }
}

function isDog(x: any): boolean {
  return x instanceof Dog;
}

function isCat(x: any): boolean {
  return x instanceof Cat;
}

const rex = new Dog("Rex", "Labrador");
const whiskers = new Cat("Whiskers");

console.log("\ninstanceof tests:");
console.log("  rex isDog:", isDog(rex));
console.log("  rex isCat:", isCat(rex));
console.log("  whiskers isDog:", isDog(whiskers));
console.log("  whiskers isCat:", isCat(whiskers));

// --- in operator ---

function hasKey(obj: Record<string, any>, key: string): boolean {
  return key in obj;
}

const withName: Record<string, any> = { name: "Alice", age: 30 };
const withoutName: Record<string, any> = { age: 25 };

console.log("\nin operator tests:");
console.log("  has 'name':", hasKey(withName, "name"));
console.log("  has 'name':", hasKey(withoutName, "name"));
console.log("  has 'age':", hasKey(withoutName, "age"));

// --- delete ---

const config: Record<string, number> = { port: 8080, timeout: 30 };
console.log("\ndelete tests:");
console.log("  before delete, has 'timeout':", hasKey(config, "timeout"));
delete config["timeout"];
console.log("  after delete, has 'timeout':", hasKey(config, "timeout"));
