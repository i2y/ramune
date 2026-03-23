// Ramune Chat Server — Hono + SQLite + WebSocket
//
// Usage:
//   ramune run -p hono examples/chat/server.js
//
// Features:
//   - REST API for messages (GET/POST/DELETE)
//   - In-memory SQLite database
//   - WebSocket real-time chat
//   - HTML chat UI at /

const { Hono } = require('hono');
const { Database } = require('bun:sqlite');

const app = new Hono();
const db = new Database(':memory:');

// Initialize database
db.run("CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, user TEXT, text TEXT, created_at TEXT)");
db.run("INSERT INTO messages (user, text, created_at) VALUES (?, ?, ?)", ["Alice", "Hello everyone!", new Date().toISOString()]);
db.run("INSERT INTO messages (user, text, created_at) VALUES (?, ?, ?)", ["Bob", "Hey Alice!", new Date().toISOString()]);

// REST API
app.get('/api/messages', (c) => {
    const messages = db.all("SELECT * FROM messages ORDER BY id DESC");
    return c.json(messages);
});

app.post('/api/messages', async (c) => {
    const body = await c.req.json();
    const result = db.run(
        "INSERT INTO messages (user, text, created_at) VALUES (?, ?, ?)",
        [body.user || "Anonymous", body.text || "", new Date().toISOString()]
    );
    const msg = db.get("SELECT * FROM messages WHERE id = ?", [result.lastInsertRowId]);
    return c.json(msg, 201);
});

app.get('/api/messages/:id', (c) => {
    const msg = db.get("SELECT * FROM messages WHERE id = ?", [Number(c.req.param('id'))]);
    if (!msg) return c.json({ error: "not found" }, 404);
    return c.json(msg);
});

app.delete('/api/messages/:id', (c) => {
    db.run("DELETE FROM messages WHERE id = ?", [Number(c.req.param('id'))]);
    return c.json({ ok: true });
});

// Chat HTML page
app.get('/', (c) => {
    return c.html(`<!DOCTYPE html>
<html><head><title>Ramune Chat</title><style>
body { font-family: system-ui; max-width: 600px; margin: 40px auto; padding: 0 20px; }
#messages { border: 1px solid #ddd; height: 300px; overflow-y: auto; padding: 10px; margin: 10px 0; border-radius: 8px; }
.msg { margin: 4px 0; }
.user { font-weight: bold; color: #2563eb; }
.time { color: #999; font-size: 0.8em; margin-left: 8px; }
input { padding: 8px; width: 70%; border: 1px solid #ddd; border-radius: 4px; }
button { padding: 8px 16px; background: #2563eb; color: white; border: none; border-radius: 4px; cursor: pointer; }
button:hover { background: #1d4ed8; }
#status { font-size: 0.85em; color: #666; margin-bottom: 8px; }
</style></head><body>
<h1>Ramune Chat</h1>
<div id="status">Connecting...</div>
<div id="messages"></div>
<input id="input" placeholder="Type a message..." onkeydown="if(event.key==='Enter')send()">
<button onclick="send()">Send</button>
<script>
var user = 'User' + Math.floor(Math.random() * 1000);
var status = document.getElementById('status');
var ws = new WebSocket('ws://' + location.host + '/ws');

ws.onopen = function() { status.textContent = 'Connected as ' + user; status.style.color = '#16a34a'; };
ws.onclose = function() { status.textContent = 'Disconnected'; status.style.color = '#dc2626'; };
ws.onmessage = function(e) {
    var msg = JSON.parse(e.data);
    var div = document.createElement('div');
    div.className = 'msg';
    var time = new Date().toLocaleTimeString();
    div.innerHTML = '<span class="user">' + msg.user + ':</span> ' + msg.text + '<span class="time">' + time + '</span>';
    document.getElementById('messages').appendChild(div);
    document.getElementById('messages').scrollTop = 999999;
};

function send() {
    var input = document.getElementById('input');
    if (!input.value) return;
    ws.send(JSON.stringify({ user: user, text: input.value }));
    input.value = '';
}
</script></body></html>`);
});

// Start server with WebSocket support
var server = Bun.serve({
    port: 3030,
    fetch: function(req, srv) {
        var url = new URL(req.url);
        if (url.pathname === '/ws') {
            if (srv.upgrade(req)) return;
            return new Response("WebSocket upgrade failed", { status: 400 });
        }
        return app.fetch(req);
    },
    websocket: {
        open: function(ws) {
            console.log("Client connected");
        },
        message: function(ws, msg) {
            var data = JSON.parse(msg);
            db.run("INSERT INTO messages (user, text, created_at) VALUES (?, ?, ?)",
                [data.user, data.text, new Date().toISOString()]);
            ws.send(msg);
        },
        close: function(ws) {
            console.log("Client disconnected");
        }
    }
});

console.log("Ramune Chat Server running on http://localhost:" + server.port);
console.log("  REST API: GET/POST/DELETE /api/messages");
console.log("  WebSocket: ws://localhost:" + server.port + "/ws");
