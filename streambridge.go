package ramune

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// streamManager manages bidirectional streams between Go and JS.
// Follows the same async I/O pattern as socketManager, processManager, etc.
type streamManager struct {
	mu      sync.Mutex
	streams map[int]*managedStream
	nextID  int
	wakeFn  func()
}

// managedStream represents a single stream bridging Go and JS.
type managedStream struct {
	id        int
	direction string // "go2js" | "js2go"

	// go2js: Go reader goroutine pushes chunks here, event loop delivers to JS.
	mu     sync.Mutex
	chunks []streamChunk
	closed bool
	err    error
	drain  chan struct{} // signaled when chunks are drained (backpressure relief)

	// js2go: JS writes chunks via Go callback, Go consumer reads from channel.
	dataCh chan []byte
	doneCh chan struct{}

	// Cancellation.
	ctx    context.Context
	cancel context.CancelFunc
}

type streamChunk struct {
	Data string `json:"data"`           // base64-encoded binary data
	Done bool   `json:"done,omitempty"` // true when stream ended
	Err  string `json:"err,omitempty"`  // error message
}

func newStreamManager() *streamManager {
	return &streamManager{
		streams: make(map[int]*managedStream),
		nextID:  1,
	}
}

// createGoToJS creates a go2js stream that reads from an io.Reader in a
// background goroutine and pushes chunks to the event queue.
func (sm *streamManager) createGoToJS(reader io.Reader) int {
	ctx, cancel := context.WithCancel(context.Background())

	sm.mu.Lock()
	id := sm.nextID
	sm.nextID++
	s := &managedStream{
		id:        id,
		direction: "go2js",
		ctx:       ctx,
		cancel:    cancel,
		drain:     make(chan struct{}, 1),
	}
	sm.streams[id] = s
	sm.mu.Unlock()

	const highWaterMark = 8 // max buffered chunks before backpressure

	go func() {
		defer func() {
			if c, ok := reader.(io.Closer); ok {
				c.Close()
			}
		}()
		buf := make([]byte, 32768) // 32KB chunks
		for {
			select {
			case <-ctx.Done():
				s.mu.Lock()
				if !s.closed {
					s.closed = true
					s.chunks = append(s.chunks, streamChunk{Done: true})
				}
				s.mu.Unlock()
				if sm.wakeFn != nil {
					sm.wakeFn()
				}
				return
			default:
			}

			// Backpressure: wait if too many chunks are buffered.
			for {
				s.mu.Lock()
				n := len(s.chunks)
				s.mu.Unlock()
				if n < highWaterMark {
					break
				}
				select {
				case <-s.drain:
				case <-ctx.Done():
					return
				}
			}

			n, err := reader.Read(buf)
			if n > 0 {
				encoded := base64.StdEncoding.EncodeToString(buf[:n])
				s.mu.Lock()
				s.chunks = append(s.chunks, streamChunk{Data: encoded})
				s.mu.Unlock()
				if sm.wakeFn != nil {
					sm.wakeFn()
				}
			}
			if err != nil {
				s.mu.Lock()
				s.closed = true
				if err == io.EOF {
					s.chunks = append(s.chunks, streamChunk{Done: true})
				} else {
					s.err = err
					s.chunks = append(s.chunks, streamChunk{Err: err.Error()})
				}
				s.mu.Unlock()
				if sm.wakeFn != nil {
					sm.wakeFn()
				}
				return
			}
		}
	}()

	return id
}

// createJSToGo creates a js2go stream. JS pushes data via __go_stream_write,
// and a Go consumer reads from dataCh.
func (sm *streamManager) createJSToGo() int {
	ctx, cancel := context.WithCancel(context.Background())

	sm.mu.Lock()
	id := sm.nextID
	sm.nextID++
	s := &managedStream{
		id:        id,
		direction: "js2go",
		dataCh:    make(chan []byte, 16),
		doneCh:    make(chan struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}
	sm.streams[id] = s
	sm.mu.Unlock()

	return id
}

// writeToStream pushes data into a js2go stream (called from JS callback).
func (sm *streamManager) writeToStream(id int, data []byte) error {
	sm.mu.Lock()
	s, ok := sm.streams[id]
	sm.mu.Unlock()
	if !ok {
		return fmt.Errorf("stream %d not found", id)
	}
	if s.direction != "js2go" {
		return fmt.Errorf("stream %d is not js2go", id)
	}
	select {
	case s.dataCh <- data:
		return nil
	case <-s.ctx.Done():
		return fmt.Errorf("stream %d cancelled", id)
	}
}

// closeStream signals the end of a js2go stream.
func (sm *streamManager) closeStream(id int) {
	sm.mu.Lock()
	s, ok := sm.streams[id]
	sm.mu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.doneCh != nil {
			close(s.doneCh)
		}
	}
	s.mu.Unlock()
}

// cancelStream cancels a stream in either direction.
func (sm *streamManager) cancelStream(id int) {
	sm.mu.Lock()
	s, ok := sm.streams[id]
	sm.mu.Unlock()
	if !ok {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.doneCh != nil {
			select {
			case <-s.doneCh:
			default:
				close(s.doneCh)
			}
		}
	}
	s.mu.Unlock()
}

func (sm *streamManager) getStream(id int) *managedStream {
	sm.mu.Lock()
	s := sm.streams[id]
	sm.mu.Unlock()
	return s
}

func (sm *streamManager) removeStream(id int) {
	sm.mu.Lock()
	if s, ok := sm.streams[id]; ok {
		if s.cancel != nil {
			s.cancel()
		}
		delete(sm.streams, id)
	}
	sm.mu.Unlock()
}

// processEvents delivers pending go2js chunks to JS.
// Must be called on the dedicated engine goroutine.
func (sm *streamManager) processEvents(r *Runtime) {
	if sm == nil {
		return
	}
	sm.mu.Lock()
	if len(sm.streams) == 0 {
		sm.mu.Unlock()
		return
	}

	type idChunks struct {
		id     int
		chunks []streamChunk
		closed bool
	}
	var all []idChunks

	for id, s := range sm.streams {
		if s.direction != "go2js" {
			continue
		}
		s.mu.Lock()
		if len(s.chunks) > 0 {
			all = append(all, idChunks{id: id, chunks: s.chunks, closed: s.closed})
			s.chunks = nil
			// Signal backpressure relief.
			if s.drain != nil {
				select {
				case s.drain <- struct{}{}:
				default:
				}
			}
		}
		s.mu.Unlock()
	}

	// Remove closed go2js streams.
	for _, ic := range all {
		if ic.closed {
			delete(sm.streams, ic.id)
		}
	}
	sm.mu.Unlock()

	if len(all) == 0 {
		return
	}

	evMap := make(map[string][]streamChunk, len(all))
	for _, ic := range all {
		evMap[itoa(ic.id)] = ic.chunks
	}
	data, _ := json.Marshal(evMap)
	r.execLocked("if(typeof __streamDeliverChunks==='function')__streamDeliverChunks(" + string(data) + ")")
}

func (sm *streamManager) hasActive() bool {
	if sm == nil {
		return false
	}
	sm.mu.Lock()
	n := len(sm.streams)
	sm.mu.Unlock()
	return n > 0
}

func (sm *streamManager) closeAll() {
	if sm == nil {
		return
	}
	sm.mu.Lock()
	for id, s := range sm.streams {
		if s.cancel != nil {
			s.cancel()
		}
		if s.doneCh != nil {
			select {
			case <-s.doneCh:
			default:
				close(s.doneCh)
			}
		}
		delete(sm.streams, id)
	}
	sm.mu.Unlock()
}

// installStreamBridge registers Go callbacks and JS delivery functions.
// Must be called with rt.mu held.
func (r *Runtime) installStreamBridge() error {
	r.streamMgr = newStreamManager()
	r.streamMgr.wakeFn = r.Wake

	if err := r.registerFuncLocked("__go_stream_create_js2go", func(args []any) (any, error) {
		id := r.streamMgr.createJSToGo()
		return float64(id), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_stream_write", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("stream_write: id and data required")
		}
		id := int(args[0].(float64))
		data, _ := args[1].(string)
		return nil, r.streamMgr.writeToStream(id, []byte(data))
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_stream_close", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("stream_close: id required")
		}
		id := int(args[0].(float64))
		r.streamMgr.closeStream(id)
		return nil, nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_stream_file", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("stream_file: path required")
		}
		path, _ := args[0].(string)
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		id := r.streamMgr.createGoToJS(f)
		return float64(id), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_stream_cancel", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("stream_cancel: id required")
		}
		id := int(args[0].(float64))
		r.streamMgr.cancelStream(id)
		return nil, nil
	}); err != nil {
		return err
	}

	return r.execLocked(streamBridgeJSSource())
}

func streamBridgeJSSource() string {
	return `
(function() {
	// Decode base64 to Uint8Array.
	function __b64toBytes(b64) {
		var binary;
		if (typeof atob === 'function') {
			binary = atob(b64);
		} else {
			var chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
			var str = b64.replace(/=+$/, '');
			binary = '';
			for (var k = 0; k < str.length; k += 4) {
				var a = chars.indexOf(str[k]), b2 = chars.indexOf(str[k+1]);
				var c = str.length > k+2 ? chars.indexOf(str[k+2]) : 64;
				var d = str.length > k+3 ? chars.indexOf(str[k+3]) : 64;
				binary += String.fromCharCode((a<<2)|(b2>>4));
				if (c !== 64) binary += String.fromCharCode(((b2&15)<<4)|(c>>2));
				if (d !== 64) binary += String.fromCharCode(((c&3)<<6)|d);
			}
		}
		var bytes = new Uint8Array(binary.length);
		for (var j = 0; j < binary.length; j++) bytes[j] = binary.charCodeAt(j);
		return bytes;
	}

	// Registry: maps stream ID to ReadableStream controller.
	var __streamControllers = {};
	// Buffer for chunks that arrive before controller is registered.
	var __streamBuffered = {};

	// Register a go2js stream's ReadableStream controller for chunk delivery.
	globalThis.__streamRegisterController = function(streamId, controller) {
		__streamControllers[streamId] = controller;
		// Deliver any buffered chunks.
		var buf = __streamBuffered[streamId];
		if (buf) {
			delete __streamBuffered[streamId];
			for (var i = 0; i < buf.length; i++) {
				var chunk = buf[i];
				if (chunk.err) {
					try { controller.error(new Error(chunk.err)); } catch(e) {}
					delete __streamControllers[streamId];
					return;
				}
				if (chunk.done) {
					try { controller.close(); } catch(e) {}
					delete __streamControllers[streamId];
					return;
				}
				if (chunk.data) {
					try { controller.enqueue(__b64toBytes(chunk.data)); } catch(e) {}
				}
			}
		}
	};

	// Unregister a stream controller.
	globalThis.__streamUnregisterController = function(streamId) {
		delete __streamControllers[streamId];
		delete __streamBuffered[streamId];
	};

	// Deliver chunks from Go to JS ReadableStream controllers.
	// Called by streamManager.processEvents() during event loop tick.
	globalThis.__streamDeliverChunks = function(eventsMap) {
		for (var id in eventsMap) {
			var controller = __streamControllers[id];
			if (!controller) {
				// Buffer chunks for later delivery when controller is registered.
				if (!__streamBuffered[id]) __streamBuffered[id] = [];
				__streamBuffered[id] = __streamBuffered[id].concat(eventsMap[id]);
				continue;
			}
			var chunks = eventsMap[id];
			for (var i = 0; i < chunks.length; i++) {
				var chunk = chunks[i];
				if (chunk.err) {
					try { controller.error(new Error(chunk.err)); } catch(e) {}
					delete __streamControllers[id];
					break;
				}
				if (chunk.done) {
					try { controller.close(); } catch(e) {}
					delete __streamControllers[id];
					break;
				}
				if (chunk.data) {
					try { controller.enqueue(__b64toBytes(chunk.data)); } catch(e) {}
				}
			}
		}
	};

	// Create a ReadableStream backed by a go2js managed stream.
	globalThis.__streamCreateReadable = function(streamId) {
		return new ReadableStream({
			start: function(controller) {
				__streamRegisterController(streamId, controller);
			},
			cancel: function() {
				__streamUnregisterController(streamId);
				__go_stream_cancel(streamId);
			}
		});
	};

	// Pump a JS ReadableStream into a js2go managed stream.
	// Used for HTTP server streaming responses.
	globalThis.__streamPumpToGo = function(stream, streamId) {
		var reader = stream.getReader();
		function pump() {
			return reader.read().then(function(result) {
				if (result.done) {
					__go_stream_close(streamId);
					return;
				}
				var data;
				if (result.value instanceof Uint8Array) {
					data = '';
					for (var i = 0; i < result.value.length; i++) {
						data += String.fromCharCode(result.value[i]);
					}
				} else {
					data = String(result.value);
				}
				__go_stream_write(streamId, data);
				return pump();
			}).catch(function(err) {
				__go_stream_close(streamId);
			});
		}
		pump();
	};
})();
`
}
