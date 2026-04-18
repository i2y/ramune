// Guestbook web app: HTML page + REST API + env.KV persistence.
//
//   cd examples/workers
//   ramune serve guestbook.ts
//   open http://localhost:3000
//
// Messages are stored per-entry under keys "msg:<timestamp>" in the
// default env.KV namespace, which is backed by SQLite via `ramune serve`.
import { Hono } from "hono";

type Message = { id: string; name: string; body: string; at: string };

const app = new Hono<{ Bindings: Env }>();

app.get("/", (c) =>
  c.html(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Ramune Guestbook</title>
<style>
  :root { color-scheme: light dark; font-family: system-ui, sans-serif; }
  body { max-width: 42rem; margin: 3rem auto; padding: 0 1rem; line-height: 1.5; }
  h1 { font-size: 1.6rem; margin-bottom: 0.25rem; }
  p.tag { color: #888; margin-top: 0; }
  form { display: grid; gap: 0.5rem; margin: 1.5rem 0; }
  input, textarea, button { font: inherit; padding: 0.5rem; border-radius: 6px;
    border: 1px solid #8884; background: transparent; color: inherit; }
  button { cursor: pointer; background: #4a90e2; color: white; border: none;
    font-weight: 600; padding: 0.6rem 1rem; }
  button:hover { background: #357ab8; }
  ul.msgs { list-style: none; padding: 0; }
  li.msg { border: 1px solid #8884; border-radius: 8px; padding: 0.75rem 1rem;
    margin: 0.75rem 0; background: #0001; }
  li.msg .meta { color: #888; font-size: 0.85rem; margin-top: 0.25rem; }
  .empty { color: #888; font-style: italic; }
</style>
</head>
<body>
<h1>📓 Ramune Guestbook</h1>
<p class="tag">A Workers-style demo: Hono + env.KV persistence.</p>

<form id="f">
  <label>Name <input name="name" required maxlength="40"></label>
  <label>Message <textarea name="body" rows="3" required maxlength="500"></textarea></label>
  <button type="submit">Sign the guestbook</button>
</form>

<h2>Messages</h2>
<ul id="list" class="msgs"><li class="empty">Loading…</li></ul>

<script>
const list = document.getElementById("list");
const form = document.getElementById("f");

async function load() {
  const r = await fetch("/api/messages");
  const data = await r.json();
  if (!data.messages.length) {
    list.innerHTML = '<li class="empty">No messages yet — be the first!</li>';
    return;
  }
  list.innerHTML = "";
  for (const m of data.messages) {
    const li = document.createElement("li");
    li.className = "msg";
    li.innerHTML =
      '<strong></strong><div></div><div class="meta"></div>';
    li.querySelector("strong").textContent = m.name;
    li.querySelector("div:nth-of-type(1)").textContent = m.body;
    li.querySelector(".meta").textContent = new Date(m.at).toLocaleString();
    list.appendChild(li);
  }
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(form);
  const r = await fetch("/api/messages", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: fd.get("name"), body: fd.get("body") }),
  });
  if (r.ok) { form.reset(); load(); }
  else alert("Post failed: " + await r.text());
});

load();
</script>
</body>
</html>`),
);

app.get("/api/messages", (c) => {
  const listing = c.env.KV.list({ prefix: "msg:", limit: 200 });
  // Newest first — keys sort ascending by timestamp, reverse for display.
  const messages: Message[] = [];
  for (const { name: key } of listing.keys.reverse()) {
    const raw = c.env.KV.get(key);
    if (raw) {
      try { messages.push(JSON.parse(raw)); } catch { /* skip */ }
    }
  }
  return c.json({ messages });
});

app.post("/api/messages", async (c) => {
  const body = await c.req.json<{ name?: string; body?: string }>();
  const name = (body.name || "").trim();
  const text = (body.body || "").trim();
  if (!name || !text) {
    return c.text("name and body required", 400);
  }
  const at = new Date().toISOString();
  const id = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const msg: Message = { id, name: name.slice(0, 40), body: text.slice(0, 500), at };
  c.env.KV.put(`msg:${id}`, msg);
  return c.json({ ok: true, message: msg });
});

export default app;
