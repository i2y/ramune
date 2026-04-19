//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
)

// goToJSPublic is the exported-to-package entry for Go->JS conversion.
// Other files prefer Runtime.goToJSLocked on the engine goroutine.
func (r *Runtime) goToJSPublic(v any) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var out *Value
	var err error
	r.dispatch(func() {
		h, e := r.goToJSLocked(v)
		if e != nil {
			err = e
			return
		}
		if isExceptionHandle(h) {
			err = r.pullExceptionLocked()
			return
		}
		out = r.wrapValue(h)
	})
	return out, err
}

// goToJSLocked converts a Go value to a JSValue handle. Primitives use
// dedicated val_from_* exports to avoid the JSON round-trip; everything
// else goes through JSON serialization + val_from_json. Struct values
// route through goToJSPublic via structToJSObject for native-instance
// semantics.
func (r *Runtime) goToJSLocked(v any) (uint64, error) {
	switch x := v.(type) {
	case nil:
		res, err := r.wzExp.valNull.Call(r.wzCtx, uint64(r.qjsCtx))
		if err != nil {
			return 0, err
		}
		return res[0], nil
	case bool:
		b := int32(0)
		if x {
			b = 1
		}
		res, err := r.wzExp.valFromBool.Call(r.wzCtx, uint64(r.qjsCtx), uint64(uint32(b)))
		if err != nil {
			return 0, err
		}
		return res[0], nil
	case int:
		return r.newInt64Locked(int64(x))
	case int32:
		return r.newInt64Locked(int64(x))
	case int64:
		return r.newInt64Locked(x)
	case uint:
		return r.newInt64Locked(int64(x))
	case uint32:
		return r.newInt64Locked(int64(x))
	case uint64:
		return r.newInt64Locked(int64(x))
	case float32:
		return r.newFloat64Locked(float64(x))
	case float64:
		return r.newFloat64Locked(x)
	case string:
		return r.newStringLocked(x)
	case []byte:
		return r.newUint8ArrayLocked(x)
	case *Value:
		if x == nil || x.rt != r {
			return 0, errors.New("ramune: cross-runtime Value")
		}
		res, err := r.wzExp.valDup.Call(r.wzCtx, uint64(r.qjsCtx), x.handle)
		if err != nil {
			return 0, err
		}
		return res[0], nil
	case *JSFunc:
		return r.jsFuncToHandleLocked(x)
	}

	// Fallback: JSON round-trip.
	rv := reflect.ValueOf(v)
	if k := rv.Kind(); k == reflect.Struct || (k == reflect.Ptr && rv.Elem().Kind() == reflect.Struct) {
		return r.structToJSObjectLocked(rv)
	}

	data, err := json.Marshal(v)
	if err != nil {
		return 0, fmt.Errorf("goToJS: marshal: %w", err)
	}
	return r.valFromJSONLocked(string(data))
}

// jsToGoLocked converts a JSValue to a Go value via JSON round-trip.
// Primitives could be short-circuited, but JSON keeps the code small and
// matches the QuickJS backend's semantics (int -> float64 normalization,
// etc.).
func (r *Runtime) jsToGoLocked(h uint64) (any, error) {
	kind := uint32(0)
	if res, err := r.wzExp.valKind.Call(r.wzCtx, uint64(r.qjsCtx), h); err == nil {
		kind = uint32(res[0])
	}
	if kind&valKindUndefined != 0 {
		return nil, nil
	}
	j, err := r.valToJSONLocked(h)
	if err != nil {
		return nil, err
	}
	if j == "" {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal([]byte(j), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// -----------------------------------------------------------------------
// Construction helpers (engine goroutine only)
// -----------------------------------------------------------------------

func (r *Runtime) newInt64Locked(n int64) (uint64, error) {
	res, err := r.wzExp.valFromInt64.Call(r.wzCtx, uint64(r.qjsCtx), uint64(n))
	if err != nil {
		return 0, err
	}
	return res[0], nil
}

func (r *Runtime) newFloat64Locked(f float64) (uint64, error) {
	res, err := r.wzExp.valFromFloat64.Call(r.wzCtx, uint64(r.qjsCtx),
		floatToAPI64(f))
	if err != nil {
		return 0, err
	}
	return res[0], nil
}

func (r *Runtime) newStringLocked(s string) (uint64, error) {
	ptr, length, err := r.writeStringLocked(s)
	if err != nil {
		return 0, err
	}
	defer r.wasmFreeLocked(ptr)
	res, err := r.wzExp.valFromString.Call(r.wzCtx, uint64(r.qjsCtx),
		uint64(ptr), uint64(length))
	if err != nil {
		return 0, err
	}
	return res[0], nil
}

func (r *Runtime) newUint8ArrayLocked(b []byte) (uint64, error) {
	if len(b) == 0 {
		return r.valFromJSONLocked("[]")
	}
	ptr, err := r.wasmMallocLocked(uint32(len(b)))
	if err != nil {
		return 0, err
	}
	defer r.wasmFreeLocked(ptr)
	if !r.wzMem.Write(ptr, b) {
		return 0, fmt.Errorf("ramune: wasm memory write out of range (len=%d)", len(b))
	}
	res, err := r.wzExp.newUint8Array.Call(r.wzCtx, uint64(r.qjsCtx),
		uint64(ptr), uint64(len(b)))
	if err != nil {
		return 0, err
	}
	return res[0], nil
}

func (r *Runtime) valFromJSONLocked(js string) (uint64, error) {
	if js == "" {
		js = "null"
	}
	ptr, length, err := r.writeStringLocked(js)
	if err != nil {
		return 0, err
	}
	defer r.wasmFreeLocked(ptr)
	res, err := r.wzExp.valFromJSON.Call(r.wzCtx, uint64(r.qjsCtx),
		uint64(ptr), uint64(length))
	if err != nil {
		return 0, err
	}
	return res[0], nil
}

// -----------------------------------------------------------------------
// Native struct bridge
// -----------------------------------------------------------------------

func (r *Runtime) structToJSObjectLocked(rv reflect.Value) (uint64, error) {
	// JSON fallback only: struct-returned values do not yet carry the
	// live getter/setter wiring that nativeReg gives the other backends.
	data, err := json.Marshal(rv.Interface())
	if err != nil {
		return 0, err
	}
	return r.valFromJSONLocked(string(data))
}

// -----------------------------------------------------------------------
// JSFunc → handle placeholder
// -----------------------------------------------------------------------

func (r *Runtime) jsFuncToHandleLocked(f *JSFunc) (uint64, error) {
	if f == nil || f.refName == "" {
		res, _ := r.wzExp.valUndefined.Call(r.wzCtx, uint64(r.qjsCtx))
		return res[0], nil
	}
	return r.globalGetPropLocked(f.refName)
}

// -----------------------------------------------------------------------
// f64 packing across the wasm boundary
// -----------------------------------------------------------------------

// wazero returns f64 results as uint64 bit patterns. These helpers
// convert each way.
func api64ToFloat(v uint64) float64 { return math.Float64frombits(v) }
func floatToAPI64(f float64) uint64 { return math.Float64bits(f) }
