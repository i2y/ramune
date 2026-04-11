var wv = new Ramune.WebView({
  title: "Ramune WebView Demo",
  width: 900,
  height: 600,
  html: `
    <!DOCTYPE html>
    <html>
    <head>
      <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
          font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
          background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
          min-height: 100vh;
          display: flex;
          align-items: center;
          justify-content: center;
        }
        .card {
          background: white;
          border-radius: 16px;
          padding: 48px;
          box-shadow: 0 20px 60px rgba(0,0,0,0.3);
          text-align: center;
          max-width: 500px;
        }
        h1 { font-size: 2em; margin-bottom: 12px; color: #333; }
        p { color: #666; margin-bottom: 24px; line-height: 1.6; }
        .badge {
          display: inline-block;
          background: linear-gradient(135deg, #667eea, #764ba2);
          color: white;
          padding: 8px 20px;
          border-radius: 20px;
          font-size: 0.9em;
          margin: 4px;
        }
        .info { margin-top: 24px; color: #999; font-size: 0.85em; }
        button {
          margin-top: 20px;
          padding: 12px 32px;
          font-size: 1em;
          border: none;
          border-radius: 8px;
          background: linear-gradient(135deg, #667eea, #764ba2);
          color: white;
          cursor: pointer;
          transition: transform 0.1s;
        }
        button:hover { transform: scale(1.05); }
        #counter { font-size: 2em; margin: 16px 0; color: #667eea; }
      </style>
    </head>
    <body>
      <div class="card">
        <h1>Ramune WebView</h1>
        <p>Native desktop window powered by<br/>
          <span class="badge">JSC / QuickJS</span>
          <span class="badge">purego</span>
          <span class="badge">WebKit</span>
        </p>
        <div id="counter">0</div>
        <button onclick="count++; document.getElementById('counter').textContent = count;">
          Click me!
        </button>
        <div class="info">
          No Cgo. No Electron. Pure Go + JavaScript.
        </div>
      </div>
      <script>var count = 0;</script>
    </body>
    </html>
  `,
  debug: true
});

wv.onclose(function() {
  console.log("WebView window closed");
});
