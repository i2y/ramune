package ramune_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/i2y/ramune"
)

func TestDgramSendReceive(t *testing.T) {
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var dgram = require('dgram');
			globalThis.__dgramReceived = [];

			// Server socket
			var server = dgram.createSocket('udp4');
			server.bind(0, function() {
				globalThis.__dgramPort = server.address().port;
			});
			server.on('message', function(msg, rinfo) {
				globalThis.__dgramReceived.push(msg);
			});
			globalThis.__dgramServer = server;
			return 'ok';
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	v.Close()

	// Wait for bind.
	var port float64
	for i := 0; i < 50; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
		pv, _ := r.Eval(`globalThis.__dgramPort || 0`)
		port, _ = pv.Float64()
		pv.Close()
		if port > 0 {
			break
		}
	}
	if port == 0 {
		t.Fatal("server did not bind")
	}

	// Send from a client socket.
	r.Exec(fmt.Sprintf(`
		var dgram = require('dgram');
		var client = dgram.createSocket('udp4');
		client.send('hello', 0, 5, %d, '127.0.0.1');
		client.send('world', 0, 5, %d, '127.0.0.1');
	`, int(port), int(port)))

	// Wait for messages to arrive.
	for i := 0; i < 50; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
		pv, _ := r.Eval(`globalThis.__dgramReceived.length`)
		count, _ := pv.Float64()
		pv.Close()
		if count >= 2 {
			break
		}
	}

	pv, _ := r.Eval(`JSON.stringify(globalThis.__dgramReceived)`)
	s, _ := pv.GoString()
	pv.Close()

	if s != `["hello","world"]` {
		t.Fatalf("expected [\"hello\",\"world\"], got %s", s)
	}

	r.Exec(`globalThis.__dgramServer.close()`)
	r.Tick()
}
