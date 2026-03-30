// Package promise provides a Promise[T] type for transpiled async/await TypeScript code.
package promise

import (
	"fmt"
	"reflect"
	"sync"
)

// Promise represents an asynchronous computation that produces a value of type T.
type Promise[T any] struct {
	ch   chan result[T]
	once sync.Once
	val  T
	err  error
}

type result[T any] struct {
	value T
	err   error
}

// New creates a Promise from an executor function (like JS new Promise((resolve, reject) => ...)).
func New[T any](executor func(resolve func(T), reject func(error))) *Promise[T] {
	p := &Promise[T]{ch: make(chan result[T], 1)}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.ch <- result[T]{err: fmt.Errorf("promise executor panicked: %v", r)}
			}
		}()
		executor(
			func(v T) { p.ch <- result[T]{value: v} },
			func(e error) { p.ch <- result[T]{err: e} },
		)
	}()
	return p
}

// Resolve creates an already-resolved Promise.
func Resolve[T any](value T) *Promise[T] {
	p := &Promise[T]{ch: make(chan result[T], 1)}
	p.ch <- result[T]{value: value}
	return p
}

// Reject creates an already-rejected Promise.
func Reject[T any](err error) *Promise[T] {
	p := &Promise[T]{ch: make(chan result[T], 1)}
	p.ch <- result[T]{err: err}
	return p
}

// Await blocks until the promise resolves and returns the value and error.
func (p *Promise[T]) Await() (T, error) {
	p.once.Do(func() {
		r := <-p.ch
		p.val = r.value
		p.err = r.err
	})
	return p.val, p.err
}

// Then chains a callback on the promise (method form). The callback receives the resolved value
// and returns the new value. Flattens Promise returns like JS .then().
func (p *Promise[T]) Then(fn any) *Promise[any] {
	return New[any](func(resolve func(any), reject func(error)) {
		v, err := p.Await()
		if err != nil {
			reject(err)
			return
		}
		// Call fn with the resolved value — fn can have various signatures
		fnVal := reflect.ValueOf(fn)
		var result []reflect.Value
		if fnVal.Type().NumIn() == 0 {
			result = fnVal.Call(nil)
		} else {
			result = fnVal.Call([]reflect.Value{reflect.ValueOf(v)})
		}
		if len(result) == 0 {
			resolve(nil)
			return
		}
		val := result[0].Interface()
		// JS Promise flattening: if callback returns a Promise, await it
		if val != nil {
			rv := reflect.ValueOf(val)
			if rv.Kind() == reflect.Ptr {
				awaitMethod := rv.MethodByName("Await")
				if awaitMethod.IsValid() {
					awaitResult := awaitMethod.Call(nil)
					if len(awaitResult) >= 2 && !awaitResult[1].IsNil() {
						reject(awaitResult[1].Interface().(error))
						return
					}
					if len(awaitResult) >= 1 {
						resolve(awaitResult[0].Interface())
						return
					}
				}
			}
		}
		resolve(val)
	})
}

// ThenFunc chains a callback, returning a new Promise (standalone form with error return).
func ThenFunc[T, U any](p *Promise[T], fn func(T) (U, error)) *Promise[U] {
	return New[U](func(resolve func(U), reject func(error)) {
		v, err := p.Await()
		if err != nil {
			reject(err)
			return
		}
		result, err := fn(v)
		if err != nil {
			reject(err)
			return
		}
		resolve(result)
	})
}

// All waits for all promises and returns their values.
func All[T any](promises ...*Promise[T]) *Promise[[]T] {
	return New[[]T](func(resolve func([]T), reject func(error)) {
		results := make([]T, len(promises))
		var wg sync.WaitGroup
		var firstErr error
		var errOnce sync.Once
		for i, p := range promises {
			wg.Add(1)
			go func(idx int, pr *Promise[T]) {
				defer wg.Done()
				v, err := pr.Await()
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
				results[idx] = v
			}(i, p)
		}
		wg.Wait()
		if firstErr != nil {
			reject(firstErr)
		} else {
			resolve(results)
		}
	})
}

// AllSlice resolves a mixed slice where elements may be *Promise[any] or plain values.
// Accepts []any or any typed slice (e.g., []*Promise[string]) via reflection.
func AllSlice(items any) *Promise[[]any] {
	var anyItems []any
	switch v := items.(type) {
	case []any:
		anyItems = v
	default:
		rv := reflect.ValueOf(items)
		if rv.Kind() != reflect.Slice {
			return Resolve[[]any]([]any{items})
		}
		anyItems = make([]any, rv.Len())
		for i := range anyItems {
			anyItems[i] = rv.Index(i).Interface()
		}
	}
	promises := make([]*Promise[any], len(anyItems))
	for i, item := range anyItems {
		if p, ok := item.(*Promise[any]); ok {
			promises[i] = p
		} else {
			promises[i] = Resolve[any](item)
		}
	}
	return All[any](promises...)
}

// Race returns the first promise to settle.
func Race[T any](promises ...*Promise[T]) *Promise[T] {
	return New[T](func(resolve func(T), reject func(error)) {
		done := make(chan struct{}, 1)
		for _, p := range promises {
			go func(pr *Promise[T]) {
				v, err := pr.Await()
				select {
				case done <- struct{}{}:
					if err != nil {
						reject(err)
					} else {
						resolve(v)
					}
				default:
				}
			}(p)
		}
	})
}
