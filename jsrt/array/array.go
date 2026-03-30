// Package array provides JavaScript Array.prototype method equivalents using Go generics.
package array

import "strings"

// Map applies fn to each element and returns a new slice.
func Map[T, U any](a []T, fn func(T, int) U) []U {
	result := make([]U, len(a))
	for i, v := range a {
		result[i] = fn(v, i)
	}
	return result
}

// Filter returns elements for which fn returns true.
func Filter[T any](a []T, fn func(T, int) bool) []T {
	var result []T
	for i, v := range a {
		if fn(v, i) {
			result = append(result, v)
		}
	}
	return result
}

// ForEach calls fn for each element.
func ForEach[T any](a []T, fn func(T, int)) {
	for i, v := range a {
		fn(v, i)
	}
}

// Reduce reduces the slice to a single value.
func Reduce[T, U any](a []T, fn func(U, T, int) U, initial U) U {
	acc := initial
	for i, v := range a {
		acc = fn(acc, v, i)
	}
	return acc
}

// Find returns the first element for which fn returns true.
func Find[T any](a []T, fn func(T, int) bool) (T, bool) {
	for i, v := range a {
		if fn(v, i) {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// FindIndex returns the index of the first element for which fn returns true, or -1.
func FindIndex[T any](a []T, fn func(T, int) bool) int {
	for i, v := range a {
		if fn(v, i) {
			return i
		}
	}
	return -1
}

// Some returns true if fn returns true for any element.
func Some[T any](a []T, fn func(T, int) bool) bool {
	for i, v := range a {
		if fn(v, i) {
			return true
		}
	}
	return false
}

// Every returns true if fn returns true for all elements.
func Every[T any](a []T, fn func(T, int) bool) bool {
	for i, v := range a {
		if !fn(v, i) {
			return false
		}
	}
	return true
}

// Includes returns true if the slice contains the item.
func Includes[T comparable](a []T, item T) bool {
	for _, v := range a {
		if v == item {
			return true
		}
	}
	return false
}

// IndexOf returns the first index of item, or -1.
func IndexOf[T comparable](a []T, item T) int {
	for i, v := range a {
		if v == item {
			return i
		}
	}
	return -1
}

// LastIndexOf returns the last index of item, or -1.
func LastIndexOf[T comparable](a []T, item T) int {
	for i := len(a) - 1; i >= 0; i-- {
		if a[i] == item {
			return i
		}
	}
	return -1
}

// Reverse returns a new slice with elements in reverse order.
func Reverse[T any](a []T) []T {
	result := make([]T, len(a))
	for i, v := range a {
		result[len(a)-1-i] = v
	}
	return result
}

// Flat flattens one level of nesting.
func Flat[T any](a [][]T) []T {
	var result []T
	for _, inner := range a {
		result = append(result, inner...)
	}
	return result
}

// Join joins elements with a separator.
func Join[T any](a []T, sep string, stringer func(T) string) string {
	if len(a) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(stringer(a[0]))
	for _, v := range a[1:] {
		b.WriteString(sep)
		b.WriteString(stringer(v))
	}
	return b.String()
}

// Slice returns a sub-slice, supporting negative indices.
func Slice[T any](a []T, start, end int) []T {
	n := len(a)
	if start < 0 {
		start = n + start
	}
	if end < 0 {
		end = n + end
	}
	if start < 0 {
		start = 0
	}
	if end > n {
		end = n
	}
	if start >= end {
		return nil
	}
	return append([]T{}, a[start:end]...)
}

// Push appends items and returns the new length.
func Push[T any](a *[]T, items ...T) int {
	*a = append(*a, items...)
	return len(*a)
}

// Pop removes and returns the last element.
func Pop[T any](a *[]T) (T, bool) {
	if len(*a) == 0 {
		var zero T
		return zero, false
	}
	last := (*a)[len(*a)-1]
	*a = (*a)[:len(*a)-1]
	return last, true
}

// Shift removes and returns the first element.
func Shift[T any](a *[]T) (T, bool) {
	if len(*a) == 0 {
		var zero T
		return zero, false
	}
	first := (*a)[0]
	*a = (*a)[1:]
	return first, true
}

// Unshift prepends items and returns the new length.
func Unshift[T any](a *[]T, items ...T) int {
	*a = append(items, *a...)
	return len(*a)
}

// Concat returns a new slice combining all input slices.
func Concat[T any](slices ...[]T) []T {
	var result []T
	for _, s := range slices {
		result = append(result, s...)
	}
	return result
}

// Splice removes deleteCount elements at start and inserts items, returning removed elements.
func Splice[T any](a *[]T, start, deleteCount int, items ...T) []T {
	n := len(*a)
	if start < 0 {
		start = n + start
	}
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	end := start + deleteCount
	if end > n {
		end = n
	}
	removed := append([]T{}, (*a)[start:end]...)
	tail := append([]T{}, (*a)[end:]...)
	*a = append((*a)[:start], items...)
	*a = append(*a, tail...)
	return removed
}
