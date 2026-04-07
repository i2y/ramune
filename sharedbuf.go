package ramune

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// sharedBuffer is a Go []byte backed shared memory buffer.
type sharedBuffer struct {
	data []byte
	// Per-int32-index wait queues for Atomics.wait/notify.
	waitMu    sync.Mutex
	waitConds map[int]*sync.Cond
}

func (sb *sharedBuffer) getCond(index int) *sync.Cond {
	sb.waitMu.Lock()
	c, ok := sb.waitConds[index]
	if !ok {
		c = sync.NewCond(&sb.waitMu)
		sb.waitConds[index] = c
	}
	sb.waitMu.Unlock()
	return c
}

// sharedBufferManager is a global registry of shared buffers.
// It must be global (not per-Runtime) so parent and worker runtimes
// can access the same underlying memory.
type sharedBufferManager struct {
	mu      sync.Mutex
	buffers map[int]*sharedBuffer
	nextID  int
}

var globalSABManager = &sharedBufferManager{
	buffers: make(map[int]*sharedBuffer),
}

func (m *sharedBufferManager) create(byteLength int) int {
	sb := &sharedBuffer{
		data:      make([]byte, byteLength),
		waitConds: make(map[int]*sync.Cond),
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.buffers[id] = sb
	m.mu.Unlock()
	return id
}

func (m *sharedBufferManager) get(id int) (*sharedBuffer, error) {
	m.mu.Lock()
	sb, ok := m.buffers[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("SharedArrayBuffer: unknown id %d", id)
	}
	return sb, nil
}

func (m *sharedBufferManager) sliceBuf(id int, begin, end int) (int, error) {
	sb, err := m.get(id)
	if err != nil {
		return 0, err
	}
	if begin < 0 {
		begin += len(sb.data)
	}
	if end < 0 {
		end += len(sb.data)
	}
	if begin < 0 {
		begin = 0
	}
	if end > len(sb.data) {
		end = len(sb.data)
	}
	if begin > end {
		begin = end
	}
	copied := make([]byte, end-begin)
	copy(copied, sb.data[begin:end])
	newSB := &sharedBuffer{
		data:      copied,
		waitConds: make(map[int]*sync.Cond),
	}
	m.mu.Lock()
	newID := m.nextID
	m.nextID++
	m.buffers[newID] = newSB
	m.mu.Unlock()
	return newID, nil
}

// int32Ptr returns a pointer to the int32 at byteOffset in the buffer.
func int32Ptr(data []byte, byteOffset int) *int32 {
	return (*int32)(unsafe.Pointer(&data[byteOffset]))
}

// Atomic operations on int32 values in shared buffers.

func sabAtomicLoad(sb *sharedBuffer, byteOffset int) int32 {
	return atomic.LoadInt32(int32Ptr(sb.data, byteOffset))
}

func sabAtomicStore(sb *sharedBuffer, byteOffset int, val int32) {
	atomic.StoreInt32(int32Ptr(sb.data, byteOffset), val)
}

func sabAtomicAdd(sb *sharedBuffer, byteOffset int, val int32) int32 {
	return atomic.AddInt32(int32Ptr(sb.data, byteOffset), val) - val
}

func sabAtomicSub(sb *sharedBuffer, byteOffset int, val int32) int32 {
	return atomic.AddInt32(int32Ptr(sb.data, byteOffset), -val) + val
}

// sabAtomicCAS applies op to the current value in a CAS loop, returning the old value.
func sabAtomicCAS(sb *sharedBuffer, byteOffset int, val int32, op func(old, val int32) int32) int32 {
	ptr := int32Ptr(sb.data, byteOffset)
	for {
		old := atomic.LoadInt32(ptr)
		if atomic.CompareAndSwapInt32(ptr, old, op(old, val)) {
			return old
		}
	}
}

func sabAtomicExchange(sb *sharedBuffer, byteOffset int, val int32) int32 {
	return atomic.SwapInt32(int32Ptr(sb.data, byteOffset), val)
}

func sabAtomicCompareExchange(sb *sharedBuffer, byteOffset int, expected, replacement int32) int32 {
	ptr := int32Ptr(sb.data, byteOffset)
	old := atomic.LoadInt32(ptr)
	atomic.CompareAndSwapInt32(ptr, expected, replacement)
	return old
}

// sabAtomicWait blocks until the int32 at byteOffset changes from value,
// or until timeout expires. Returns "ok", "not-equal", or "timed-out".
func sabAtomicWait(sb *sharedBuffer, byteOffset int, value int32, timeoutMs float64) string {
	current := atomic.LoadInt32(int32Ptr(sb.data, byteOffset))
	if current != value {
		return "not-equal"
	}

	index := byteOffset / 4
	cond := sb.getCond(index)

	if timeoutMs == 0 {
		return "timed-out"
	}

	var cancelled int32
	done := make(chan string, 1)
	go func() {
		cond.L.Lock()
		for atomic.LoadInt32(int32Ptr(sb.data, byteOffset)) == value && atomic.LoadInt32(&cancelled) == 0 {
			cond.Wait()
		}
		cond.L.Unlock()
		if atomic.LoadInt32(&cancelled) != 0 {
			return
		}
		done <- "ok"
	}()

	if timeoutMs < 0 || math.IsInf(timeoutMs, 1) {
		return <-done
	}

	select {
	case result := <-done:
		return result
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		atomic.StoreInt32(&cancelled, 1)
		cond.Broadcast()
		return "timed-out"
	}
}

// sabAtomicNotify wakes up to count waiters on the int32 at byteOffset.
func sabAtomicNotify(sb *sharedBuffer, byteOffset int, count int) int {
	index := byteOffset / 4
	cond := sb.getCond(index)
	if count <= 0 {
		return 0
	}
	// Broadcast wakes all waiters; we can't selectively wake N.
	// This is a simplification — real engines track individual waiters.
	cond.Broadcast()
	return count
}

// installSharedArrayBuffer registers SharedArrayBuffer and Atomics polyfills.
func (r *Runtime) installSharedArrayBuffer() error {
	mgr := globalSABManager

	if err := r.registerFuncLocked("__go_sab_create", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("SharedArrayBuffer: byteLength required")
		}
		byteLength, _ := args[0].(float64)
		id := mgr.create(int(byteLength))
		return float64(id), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_sab_slice", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("SharedArrayBuffer.slice: id, begin, end required")
		}
		id, _ := args[0].(float64)
		begin, _ := args[1].(float64)
		end, _ := args[2].(float64)
		newID, err := mgr.sliceBuf(int(id), int(begin), int(end))
		if err != nil {
			return nil, err
		}
		return float64(newID), nil
	}); err != nil {
		return err
	}

	// Atomics: load/store/add/sub/and/or/xor/exchange/compareExchange
	if err := r.registerFuncLocked("__go_atomics_op", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("Atomics: op, sabId, byteOffset required")
		}
		op, _ := args[0].(string)
		sabID, _ := args[1].(float64)
		byteOffset, _ := args[2].(float64)

		sb, err := mgr.get(int(sabID))
		if err != nil {
			return nil, err
		}

		off := int(byteOffset)
		if off < 0 || off+4 > len(sb.data) {
			return nil, fmt.Errorf("Atomics: index out of range")
		}

		var val int32
		if len(args) > 3 {
			val = int32(args[3].(float64))
		}

		switch op {
		case "load":
			return float64(sabAtomicLoad(sb, off)), nil
		case "store":
			sabAtomicStore(sb, off, val)
			return float64(val), nil
		case "add":
			return float64(sabAtomicAdd(sb, off, val)), nil
		case "sub":
			return float64(sabAtomicSub(sb, off, val)), nil
		case "and":
			return float64(sabAtomicCAS(sb, off, val, func(a, b int32) int32 { return a & b })), nil
		case "or":
			return float64(sabAtomicCAS(sb, off, val, func(a, b int32) int32 { return a | b })), nil
		case "xor":
			return float64(sabAtomicCAS(sb, off, val, func(a, b int32) int32 { return a ^ b })), nil
		case "exchange":
			return float64(sabAtomicExchange(sb, off, val)), nil
		case "compareExchange":
			if len(args) < 5 {
				return nil, fmt.Errorf("Atomics.compareExchange: expected and replacement required")
			}
			replacement := int32(args[4].(float64))
			return float64(sabAtomicCompareExchange(sb, off, val, replacement)), nil
		default:
			return nil, fmt.Errorf("Atomics: unknown op %q", op)
		}
	}); err != nil {
		return err
	}

	// Atomics.wait — must run in a goroutine to avoid blocking the JSC thread.
	// Called synchronously from worker threads (not main thread).
	if err := r.registerFuncLocked("__go_atomics_wait", func(args []any) (any, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("Atomics.wait: sabId, byteOffset, value, timeout required")
		}
		sabID, _ := args[0].(float64)
		byteOffset, _ := args[1].(float64)
		value, _ := args[2].(float64)
		timeout, _ := args[3].(float64)

		sb, err := mgr.get(int(sabID))
		if err != nil {
			return nil, err
		}

		off := int(byteOffset)
		if off < 0 || off+4 > len(sb.data) {
			return nil, fmt.Errorf("Atomics.wait: index out of range")
		}

		result := sabAtomicWait(sb, off, int32(value), timeout)
		return result, nil
	}); err != nil {
		return err
	}

	// Atomics.notify
	if err := r.registerFuncLocked("__go_atomics_notify", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("Atomics.notify: sabId, byteOffset, count required")
		}
		sabID, _ := args[0].(float64)
		byteOffset, _ := args[1].(float64)
		count, _ := args[2].(float64)

		sb, err := mgr.get(int(sabID))
		if err != nil {
			return nil, err
		}

		off := int(byteOffset)
		if off < 0 || off+4 > len(sb.data) {
			return nil, fmt.Errorf("Atomics.notify: index out of range")
		}

		n := sabAtomicNotify(sb, off, int(count))
		return float64(n), nil
	}); err != nil {
		return err
	}

	// Non-atomic bulk read/write for typed array element access.
	if err := r.registerFuncLocked("__go_sab_get_elements", func(args []any) (any, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("sab_get_elements: sabId, byteOffset, count, bytesPerElement required")
		}
		sabID, _ := args[0].(float64)
		byteOffset, _ := args[1].(float64)
		count, _ := args[2].(float64)
		bpe, _ := args[3].(float64)

		sb, err := mgr.get(int(sabID))
		if err != nil {
			return nil, err
		}

		off := int(byteOffset)
		n := int(count)
		bytesPerElem := int(bpe)
		end := off + n*bytesPerElem
		if end > len(sb.data) {
			end = len(sb.data)
		}

		vals := make([]float64, n)
		for i := 0; i < n; i++ {
			pos := off + i*bytesPerElem
			if pos+bytesPerElem > len(sb.data) {
				break
			}
			switch bytesPerElem {
			case 1:
				vals[i] = float64(sb.data[pos])
			case 2:
				vals[i] = float64(binary.LittleEndian.Uint16(sb.data[pos:]))
			case 4:
				vals[i] = float64(int32(binary.LittleEndian.Uint32(sb.data[pos:])))
			case 8:
				vals[i] = math.Float64frombits(binary.LittleEndian.Uint64(sb.data[pos:]))
			}
		}
		b, _ := json.Marshal(vals)
		return string(b), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_sab_set_element", func(args []any) (any, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("sab_set_element: sabId, byteOffset, bytesPerElement, value required")
		}
		sabID, _ := args[0].(float64)
		byteOffset, _ := args[1].(float64)
		bpe, _ := args[2].(float64)
		value, _ := args[3].(float64)

		sb, err := mgr.get(int(sabID))
		if err != nil {
			return nil, err
		}

		off := int(byteOffset)
		bytesPerElem := int(bpe)
		if off+bytesPerElem > len(sb.data) {
			return nil, fmt.Errorf("sab_set_element: out of range")
		}

		switch bytesPerElem {
		case 1:
			sb.data[off] = byte(int8(value))
		case 2:
			binary.LittleEndian.PutUint16(sb.data[off:], uint16(int16(value)))
		case 4:
			binary.LittleEndian.PutUint32(sb.data[off:], uint32(int32(value)))
		case 8:
			binary.LittleEndian.PutUint64(sb.data[off:], math.Float64bits(value))
		}
		return nil, nil
	}); err != nil {
		return err
	}

	return r.execLocked(sharedArrayBufferJSSource())
}

func sharedArrayBufferJSSource() string {
	return strings.TrimSpace(`
(function() {
	// --- SharedArrayBuffer ---
	function SharedArrayBuffer(byteLength) {
		if (!(this instanceof SharedArrayBuffer)) return new SharedArrayBuffer(byteLength);
		this._sabId = __go_sab_create(byteLength);
		this.byteLength = byteLength;
	}
	SharedArrayBuffer.prototype.slice = function(begin, end) {
		if (begin === undefined) begin = 0;
		if (end === undefined) end = this.byteLength;
		var newId = __go_sab_slice(this._sabId, begin, end);
		var sab = Object.create(SharedArrayBuffer.prototype);
		sab._sabId = newId;
		sab.byteLength = (end < 0 ? this.byteLength + end : end) - (begin < 0 ? this.byteLength + begin : begin);
		if (sab.byteLength < 0) sab.byteLength = 0;
		return sab;
	};
	SharedArrayBuffer.prototype[Symbol.toStringTag] = 'SharedArrayBuffer';
	globalThis.SharedArrayBuffer = SharedArrayBuffer;

	// --- SharedTypedArray: typed view over SharedArrayBuffer ---
	// SharedTypedArray uses Proxy for O(1) construction — indexed reads/writes
	// go through Go callbacks without defining a getter/setter per index.
	function makeSharedTypedArray(bytesPerElement, buffer, byteOffset, length) {
		var target = {
			buffer: buffer,
			BYTES_PER_ELEMENT: bytesPerElement,
			byteOffset: byteOffset || 0,
			_sabId: buffer._sabId
		};
		target.length = length !== undefined ? length : (buffer.byteLength - target.byteOffset) / bytesPerElement;
		target.byteLength = target.length * bytesPerElement;

		target.set = function(array, offset) {
			offset = offset || 0;
			for (var i = 0; i < array.length; i++) proxy[offset + i] = array[i];
		};
		target.subarray = function(begin, end) {
			if (begin === undefined) begin = 0;
			if (end === undefined) end = target.length;
			if (begin < 0) begin = target.length + begin;
			if (end < 0) end = target.length + end;
			return makeSharedTypedArray(bytesPerElement, buffer, target.byteOffset + begin * bytesPerElement, end - begin);
		};

		var proxy = new Proxy(target, {
			get: function(obj, prop) {
				if (typeof prop === 'string' && prop === String(prop >>> 0) && (prop >>> 0) < obj.length) {
					return __go_atomics_op('load', obj._sabId, obj.byteOffset + (prop >>> 0) * obj.BYTES_PER_ELEMENT);
				}
				if (prop === Symbol.iterator) {
					return function() {
						var i = 0;
						return { next: function() {
							if (i >= obj.length) return { done: true };
							return { value: proxy[i++], done: false };
						}};
					};
				}
				return obj[prop];
			},
			set: function(obj, prop, value) {
				if (typeof prop === 'string' && prop === String(prop >>> 0) && (prop >>> 0) < obj.length) {
					__go_sab_set_element(obj._sabId, obj.byteOffset + (prop >>> 0) * obj.BYTES_PER_ELEMENT, obj.BYTES_PER_ELEMENT, value);
					return true;
				}
				obj[prop] = value;
				return true;
			}
		});
		return proxy;
	}

	// Wrap TypedArray constructors to detect SharedArrayBuffer.
	var _types = {
		Int8Array:    { orig: globalThis.Int8Array,    bpe: 1 },
		Uint8Array:   { orig: globalThis.Uint8Array,   bpe: 1 },
		Int16Array:   { orig: globalThis.Int16Array,   bpe: 2 },
		Uint16Array:  { orig: globalThis.Uint16Array,  bpe: 2 },
		Int32Array:   { orig: globalThis.Int32Array,    bpe: 4 },
		Uint32Array:  { orig: globalThis.Uint32Array,  bpe: 4 },
		Float32Array: { orig: globalThis.Float32Array,  bpe: 4 },
		Float64Array: { orig: globalThis.Float64Array,  bpe: 8 }
	};

	Object.keys(_types).forEach(function(name) {
		var info = _types[name];
		var Orig = info.orig;
		globalThis[name] = function(bufferOrLength, byteOffset, length) {
			if (bufferOrLength && bufferOrLength._sabId !== undefined) {
				return makeSharedTypedArray(info.bpe, bufferOrLength, byteOffset, length);
			}
			if (arguments.length === 3) return new Orig(bufferOrLength, byteOffset, length);
			if (arguments.length === 2) return new Orig(bufferOrLength, byteOffset);
			return new Orig(bufferOrLength);
		};
		globalThis[name].BYTES_PER_ELEMENT = info.bpe;
		globalThis[name].from = Orig.from ? Orig.from.bind(Orig) : undefined;
		globalThis[name].of = Orig.of ? Orig.of.bind(Orig) : undefined;
	});

	// --- Atomics ---
	globalThis.Atomics = {
		load: function(ta, index) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			return __go_atomics_op('load', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT);
		},
		store: function(ta, index, value) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			__go_atomics_op('store', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, value);
			return value;
		},
		add: function(ta, index, value) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			return __go_atomics_op('add', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, value);
		},
		sub: function(ta, index, value) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			return __go_atomics_op('sub', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, value);
		},
		and: function(ta, index, value) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			return __go_atomics_op('and', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, value);
		},
		or: function(ta, index, value) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			return __go_atomics_op('or', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, value);
		},
		xor: function(ta, index, value) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			return __go_atomics_op('xor', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, value);
		},
		exchange: function(ta, index, value) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			return __go_atomics_op('exchange', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, value);
		},
		compareExchange: function(ta, index, expected, replacement) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics: not a shared typed array');
			return __go_atomics_op('compareExchange', ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, expected, replacement);
		},
		wait: function(ta, index, value, timeout) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics.wait: not a shared typed array');
			if (timeout === undefined) timeout = -1;
			return __go_atomics_wait(ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, value, timeout);
		},
		notify: function(ta, index, count) {
			if (!ta._sabId && ta._sabId !== 0) throw new TypeError('Atomics.notify: not a shared typed array');
			if (count === undefined) count = Infinity;
			return __go_atomics_notify(ta._sabId, ta.byteOffset + index * ta.BYTES_PER_ELEMENT, count);
		},
		isLockFree: function(size) {
			return size === 1 || size === 2 || size === 4 || size === 8;
		}
	};
})();
`)
}
