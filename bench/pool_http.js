// Pool HTTP benchmark — used by pool_bench.go
// This file defines the __poolHandle function for benchmarking.
globalThis.__poolHandle = function(req) {
    return {
        status: 200,
        headers: { "content-type": "text/plain" },
        body: "Hello, World!"
    };
};
