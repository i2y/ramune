const http = require('http');
const server = http.createServer((req, res) => {
    res.writeHead(200, {'content-type': 'application/json'});
    res.end(JSON.stringify({hello: "world"}));
});
server.listen(3002, () => console.log("node listening on port 3002"));
