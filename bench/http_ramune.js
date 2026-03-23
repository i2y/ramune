Bun.serve({
    port: 3001,
    fetch: function(req) {
        return {
            status: 200,
            headers: {"content-type": "application/json"},
            body: JSON.stringify({hello: "world"})
        };
    }
});
console.log("ramune listening on port 3001");
