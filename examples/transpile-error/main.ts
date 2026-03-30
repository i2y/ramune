// Error handling: try/catch/finally, throw

function divide(a: number, b: number): number {
  if (b === 0) {
    throw new Error("division by zero");
  }
  return a / b;
}

function safeDivide(a: number, b: number): string {
  try {
    const result = divide(a, b);
    return `${a} / ${b} = ${result}`;
  } catch (e) {
    return `Error: cannot divide ${a} by ${b}`;
  }
}

// Successful division
console.log(safeDivide(10, 3));
console.log(safeDivide(42, 6));

// Division by zero — caught
console.log(safeDivide(5, 0));

// Try/finally
console.log("\nWith finally:");
try {
  console.log("  trying...");
  console.log("  result:", divide(10, 2));
} catch (e) {
  console.log("  caught error");
} finally {
  console.log("  cleanup done");
}
