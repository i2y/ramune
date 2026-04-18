// Hello-world with Hono. Requires `hono` as a dependency — this
// directory's ramune.toml declares it.
//
//   cd examples/workers
//   ramune serve hono-hello.ts
//   curl http://localhost:3000/
//   curl http://localhost:3000/api/hello?name=Bob
//
// Accessing the Hono instance the Workers way: the default export is
// just Hono's own fetch adapter ({ fetch }).
import { Hono } from "hono";

const app = new Hono();

app.get("/", (c) =>
  c.html(`<h1>Hono + Ramune Workers</h1>
<p>Try <a href="/api/hello?name=Ada">/api/hello?name=Ada</a>.</p>`),
);

app.get("/api/hello", (c) => {
  const name = c.req.query("name") ?? "world";
  return c.json({ message: `Hello, ${name}!`, style: "hono" });
});

export default app;
