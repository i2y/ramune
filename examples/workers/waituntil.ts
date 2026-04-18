// Demonstrates ctx.waitUntil: HTTP response returns immediately while
// the background promise keeps running.
//   curl http://localhost:3000/api/waituntil
let lastBackgroundAt: string | null = null;

export default {
  route: "/api/waituntil",

  async fetch(_request: Request, _env: unknown, ctx: { waitUntil(p: Promise<unknown>): void }): Promise<Response> {
    ctx.waitUntil(
      new Promise<void>((resolve) => {
        setTimeout(() => {
          lastBackgroundAt = new Date().toISOString();
          console.log("[waituntil] background task finished at", lastBackgroundAt);
          resolve();
        }, 1000);
      }),
    );
    return Response.json({ queued: true, lastBackgroundAt });
  },
};
