// Streaming response via ReadableStream — Server-Sent Events.
//   curl -N http://localhost:3000/api/sse
export default {
  route: "/api/sse",

  async fetch(): Promise<Response> {
    const encoder = new TextEncoder();
    const stream = new ReadableStream({
      async start(controller) {
        for (let i = 0; i < 5; i++) {
          controller.enqueue(
            encoder.encode(`data: tick ${i} at ${new Date().toISOString()}\n\n`),
          );
          await new Promise((r) => setTimeout(r, 300));
        }
        controller.close();
      },
    });
    return new Response(stream, {
      headers: {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
      },
    });
  },
};
