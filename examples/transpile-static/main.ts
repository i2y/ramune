// Static class members example

class Counter {
  static count: number = 0;

  static increment(): void {
    Counter.count = Counter.count + 1;
  }

  static getCount(): number {
    return Counter.count;
  }

  static reset(): void {
    Counter.count = 0;
  }
}

class MathUtil {
  static PI: number = 3.14159;

  static circleArea(radius: number): number {
    return MathUtil.PI * radius * radius;
  }

  static add(a: number, b: number): number {
    return a + b;
  }

  static max(a: number, b: number): number {
    if (a > b) {
      return a;
    }
    return b;
  }
}

// Use Counter
console.log("Counter tests:");
console.log("  initial count:", Counter.getCount());

Counter.increment();
Counter.increment();
Counter.increment();
console.log("  after 3 increments:", Counter.getCount());

Counter.reset();
console.log("  after reset:", Counter.getCount());

// Use MathUtil
console.log("\nMathUtil tests:");
console.log("  PI:", MathUtil.PI);
console.log("  circleArea(5):", MathUtil.circleArea(5));
console.log("  add(10, 20):", MathUtil.add(10, 20));
console.log("  max(42, 17):", MathUtil.max(42, 17));
