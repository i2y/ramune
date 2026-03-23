Bun.serve({
    port: 3003,
    fetch(req) {
        return new Response(JSON.stringify({hello: "world"}), {
            headers: {"content-type": "application/json"}
        });
    }
});
console.log("bun listening on port 3003");
