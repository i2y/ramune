// Hybrid-friendly: export so the picker can extract; defer the call so
// the postlude shim has time to swap `globalThis.fib` to the native Go
// version before it runs. Without the setTimeout(0), the local JS binding
// from the function declaration is used and hybrid extraction is wasted.
export function fib(n: number): number {
    if (n <= 1) return n;
    return fib(n - 1) + fib(n - 2);
}
setTimeout(() => {
    console.log((globalThis as any).fib(40));
}, 0);
