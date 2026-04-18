// Minimal Workers-style handler.
// Run with: ramune serve examples/workers/hello.ts
//   curl http://localhost:3000/api/hello?name=Alice
export default {
  route: "/api/hello",

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const name = url.searchParams.get("name") || "world";
    return Response.json({
      message: `Hello, ${name}!`,
      method: request.method,
      style: "workers",
    });
  },
};
