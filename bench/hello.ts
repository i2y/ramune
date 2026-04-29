// Empty-work TS for startup-only measurement: load runtime, parse
// bundle, run the postlude shim, exit. Subtracting this from fib.ts's
// total isolates the actual compute time from the binary's fixed costs.
console.log("hello");
