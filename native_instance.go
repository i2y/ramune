package ramune

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// nativeTypeRegistry manages per-type callback registration and per-instance data.
// Callbacks are registered once per type, instances are tracked by ID.
// Instances are automatically released when the JS object is garbage collected
// via FinalizationRegistry (if available). Falls back to clearInstances() on Runtime.Close().
type nativeTypeRegistry struct {
	mu        sync.RWMutex
	types     map[reflect.Type]*nativeTypeInfo
	instances map[int64]reflect.Value // instance ID → pointer to struct
	nextID    int64
	setupOnce sync.Once
}

type nativeTypeInfo struct {
	once       sync.Once
	prefix     string // e.g., "__nt_pkgpath_Counter_"
	fields     []nativeFieldInfo
	methods    []nativeMethodInfo
	jsTemplate string // cached JS template with __INSTANCE_ID__ placeholder
}

type nativeFieldInfo struct {
	index  int
	jsName string
}

type nativeMethodInfo struct {
	index  int
	jsName string
}

func newNativeTypeRegistry() *nativeTypeRegistry {
	return &nativeTypeRegistry{
		types:     make(map[reflect.Type]*nativeTypeInfo),
		instances: make(map[int64]reflect.Value),
	}
}

// registerInstance stores a struct pointer and returns its instance ID.
func (reg *nativeTypeRegistry) registerInstance(ptr reflect.Value) int64 {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.nextID++
	id := reg.nextID
	reg.instances[id] = ptr
	return id
}

// getInstance retrieves a struct pointer by instance ID.
func (reg *nativeTypeRegistry) getInstance(id int64) (reflect.Value, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	v, ok := reg.instances[id]
	return v, ok
}

// releaseInstance removes an instance from the registry.
func (reg *nativeTypeRegistry) releaseInstance(id int64) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	delete(reg.instances, id)
}

// instanceCount returns the number of live instances.
func (reg *nativeTypeRegistry) instanceCount() int {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return len(reg.instances)
}

// clearInstances removes all instances from the registry.
func (reg *nativeTypeRegistry) clearInstances() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.instances = make(map[int64]reflect.Value)
}

// ensureTypeRegistered registers per-type callbacks if not already done.
func (reg *nativeTypeRegistry) ensureTypeRegistered(r *Runtime, t reflect.Type) *nativeTypeInfo {
	reg.mu.Lock()
	info, exists := reg.types[t]
	if !exists {
		// Use PkgPath to avoid collision between same-named types in different packages.
		sanitized := strings.NewReplacer("/", "_", ".", "_", "-", "_").Replace(t.PkgPath())
		info = &nativeTypeInfo{
			prefix: fmt.Sprintf("__nt_%s_%s_", sanitized, t.Name()),
		}
		reg.types[t] = info
	}
	reg.mu.Unlock()

	info.once.Do(func() {
		reg.registerType(r, info, t)
	})
	return info
}

func (reg *nativeTypeRegistry) registerType(r *Runtime, info *nativeTypeInfo, t reflect.Type) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		jsName := fieldJSName(field)
		info.fields = append(info.fields, nativeFieldInfo{index: i, jsName: jsName})
	}

	ptrType := reflect.PointerTo(t)
	for i := 0; i < ptrType.NumMethod(); i++ {
		method := ptrType.Method(i)
		if !method.IsExported() {
			continue
		}
		if method.Type.NumIn() == 1 && method.Type.NumOut() == 0 {
			continue
		}
		if strings.HasPrefix(method.Name, "Get") && method.Type.NumIn() == 1 && method.Type.NumOut() == 1 {
			continue
		}
		info.methods = append(info.methods, nativeMethodInfo{
			index:  i,
			jsName: toCamelCase(method.Name),
		})
	}

	for _, f := range info.fields {
		fInfo := f
		r.registerFuncLocked(info.prefix+"get_"+fInfo.jsName, func(args []any) (any, error) {
			if len(args) < 1 {
				return nil, nil
			}
			id, _ := toFloat64(args[0])
			inst, ok := reg.getInstance(int64(id))
			if !ok {
				return nil, nil
			}
			return reflectToAny(inst.Elem().Field(fInfo.index)), nil
		})
		r.registerFuncLocked(info.prefix+"set_"+fInfo.jsName, func(args []any) (any, error) {
			if len(args) < 2 {
				return nil, nil
			}
			id, _ := toFloat64(args[0])
			inst, ok := reg.getInstance(int64(id))
			if !ok {
				return nil, nil
			}
			if err := setReflectValue(inst.Elem().Field(fInfo.index), args[1]); err != nil {
				return nil, err
			}
			return nil, nil
		})
	}

	for _, m := range info.methods {
		mInfo := m
		r.registerFuncLocked(info.prefix+"m_"+mInfo.jsName, func(args []any) (result any, retErr error) {
			defer func() {
				if rec := recover(); rec != nil {
					retErr = fmt.Errorf("panic in %s: %v", mInfo.jsName, rec)
				}
			}()
			if len(args) < 1 {
				return nil, nil
			}
			id, _ := toFloat64(args[0])
			inst, ok := reg.getInstance(int64(id))
			if !ok {
				return nil, fmt.Errorf("instance not found")
			}
			methodVal := inst.Method(mInfo.index)
			mType := methodVal.Type()
			methodArgs := args[1:]
			in := make([]reflect.Value, mType.NumIn())
			for j := 0; j < mType.NumIn(); j++ {
				if j < len(methodArgs) {
					converted, err := convertArg(methodArgs[j], mType.In(j))
					if err != nil {
						return nil, fmt.Errorf("%s: arg %d: %w", mInfo.jsName, j, err)
					}
					in[j] = converted
				} else {
					in[j] = reflect.Zero(mType.In(j))
				}
			}
			out := methodVal.Call(in)
			switch len(out) {
			case 0:
				return nil, nil
			case 1:
				v := out[0]
				if v.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
					if !v.IsNil() {
						return nil, v.Interface().(error)
					}
					return nil, nil
				}
				return reflectToAny(v), nil
			case 2:
				if !out[1].IsNil() {
					return nil, out[1].Interface().(error)
				}
				return reflectToAny(out[0]), nil
			default:
				return reflectToAny(out[0]), nil
			}
		})
	}

	info.buildJSTemplate()
}

const jsInstanceIDPlaceholder = "__INSTANCE_ID__"

func (info *nativeTypeInfo) buildJSTemplate() {
	var sb strings.Builder
	sb.WriteString("(function(){var o={};var _id=")
	sb.WriteString(jsInstanceIDPlaceholder)
	sb.WriteString(";")

	for _, f := range info.fields {
		sb.WriteString("Object.defineProperty(o,\"")
		sb.WriteString(f.jsName)
		sb.WriteString("\",{get:function(){return ")
		sb.WriteString(info.prefix)
		sb.WriteString("get_")
		sb.WriteString(f.jsName)
		sb.WriteString("(_id)},set:function(v){")
		sb.WriteString(info.prefix)
		sb.WriteString("set_")
		sb.WriteString(f.jsName)
		sb.WriteString("(_id,v)},enumerable:true,configurable:true});")
	}

	for _, m := range info.methods {
		sb.WriteString("o[\"")
		sb.WriteString(m.jsName)
		sb.WriteString("\"]=function(){var a=[_id];for(var i=0;i<arguments.length;i++)a.push(arguments[i]);return ")
		sb.WriteString(info.prefix)
		sb.WriteString("m_")
		sb.WriteString(m.jsName)
		sb.WriteString(".apply(null,a)};")
	}

	sb.WriteString("if(globalThis.__nativeInstanceRegistry)globalThis.__nativeInstanceRegistry.register(o,_id);")
	sb.WriteString("return o;})()")
	info.jsTemplate = sb.String()
}

// ensureNativeReg initializes the native type registry if not already created.
func (r *Runtime) ensureNativeReg() {
	if r.nativeReg == nil {
		r.nativeReg = newNativeTypeRegistry()
	}
}

// installNativeReleaseBridge registers the Go callback and JS FinalizationRegistry
// used to automatically release native instances when their JS wrapper is GC'd.
// Must be called from module Init (not from within a GoFunc callback).
func (r *Runtime) installNativeReleaseBridge() {
	r.ensureNativeReg()
	reg := r.nativeReg
	reg.setupOnce.Do(func() {
		if err := r.registerFuncLocked("__nativeRelease", func(args []any) (any, error) {
			if len(args) < 1 {
				return nil, nil
			}
			id, _ := toFloat64(args[0])
			reg.releaseInstance(int64(id))
			return nil, nil
		}); err != nil {
			return
		}
		// Install the JS-side FinalizationRegistry hook (backend-specific). On
		// backends whose GC doesn't fire FR callbacks synchronously during
		// allocation (JSC / modernc quickjs / goja), FR is wired to
		// __nativeRelease so JS GC decrements the Go registry. On qjswasm
		// (fastschema/qjs) the GC is aggressive enough that FR would fire
		// during the creation loop itself, making NativeInstanceCount
		// impossible to observe; there we skip FR and rely on Runtime.Close()
		// / explicit __nativeRelease calls. This matches the CLAUDE.md
		// documented behavior ("struct instances returned to JS are not freed
		// on JS GC").
		r.installFinalizationRegistryHook()
	})
}

// NativeInstanceCount returns the number of live native struct instances.
// Useful for testing and debugging instance lifecycle.
func (r *Runtime) NativeInstanceCount() int {
	if r.nativeReg == nil {
		return 0
	}
	return r.nativeReg.instanceCount()
}

// generateJSObject returns the JS IIFE code for creating an instance object.
func (info *nativeTypeInfo) generateJSObject(instanceID int64) string {
	return strings.Replace(info.jsTemplate, jsInstanceIDPlaceholder, strconv.FormatInt(instanceID, 10), 1)
}
