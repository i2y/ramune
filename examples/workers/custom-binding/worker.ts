/// <reference path="./workers-env.d.ts" />
//
// The worker uses env.EMAIL just like it would use env.KV: Ramune
// has no built-in email binding, but the Go side registered one and
// injected the facade via WithExtraEnvJS, so the JS here is oblivious
// to how it's implemented.
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (request.method === "POST" && new URL(request.url).pathname === "/notify") {
      const { user, body } = await request.json<{ user: string; body: string }>();
      await env.EMAIL.send({
        to: user,
        subject: "Hello from Ramune",
        body,
      });
      return Response.json({ ok: true, delivered_to: user });
    }
    return new Response(
      "POST /notify {user, body} — check the Go server log for [EMAIL] output\n",
      { headers: { "Content-Type": "text/plain" } },
    );
  },
} satisfies WorkersHandler;
