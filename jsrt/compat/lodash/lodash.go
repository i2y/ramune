// Package lodash provides Go adapters for commonly used lodash/lodash-es functions.
package lodash

import (
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/i2y/ramune/jsrt/array"
)

// Chunk splits a slice into groups of the given size.
func Chunk[T any](arr []T, size int) [][]T {
	if size <= 0 {
		return nil
	}
	var result [][]T
	for i := 0; i < len(arr); i += size {
		end := i + size
		if end > len(arr) {
			end = len(arr)
		}
		result = append(result, append([]T{}, arr[i:end]...))
	}
	return result
}

// Flatten flattens one level of nesting.
func Flatten[T any](arr [][]T) []T {
	return array.Flat(arr)
}

// Uniq returns unique elements.
func Uniq[T comparable](arr []T) []T {
	seen := make(map[T]bool)
	var result []T
	for _, v := range arr {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}

// UniqBy returns unique elements using a key function.
func UniqBy[T any, K comparable](arr []T, fn func(T) K) []T {
	seen := make(map[K]bool)
	var result []T
	for _, v := range arr {
		key := fn(v)
		if !seen[key] {
			seen[key] = true
			result = append(result, v)
		}
	}
	return result
}

// GroupBy groups elements by a key function.
func GroupBy[T any, K comparable](arr []T, fn func(T) K) map[K][]T {
	result := make(map[K][]T)
	for _, v := range arr {
		key := fn(v)
		result[key] = append(result[key], v)
	}
	return result
}

// KeyBy creates a map keyed by the result of a function.
func KeyBy[T any, K comparable](arr []T, fn func(T) K) map[K]T {
	result := make(map[K]T)
	for _, v := range arr {
		result[fn(v)] = v
	}
	return result
}

// SortBy sorts a slice by a numeric key function.
func SortBy[T any](arr []T, fn func(T) float64) []T {
	result := append([]T{}, arr...)
	sort.Slice(result, func(i, j int) bool {
		return fn(result[i]) < fn(result[j])
	})
	return result
}

// Compact removes zero-value elements.
func Compact[T comparable](arr []T) []T {
	var zero T
	var result []T
	for _, v := range arr {
		if v != zero {
			result = append(result, v)
		}
	}
	return result
}

// Difference returns elements in a that are not in others.
func Difference[T comparable](a []T, others ...[]T) []T {
	exclude := make(map[T]bool)
	for _, other := range others {
		for _, v := range other {
			exclude[v] = true
		}
	}
	var result []T
	for _, v := range a {
		if !exclude[v] {
			result = append(result, v)
		}
	}
	return result
}

// Intersection returns elements present in all slices.
func Intersection[T comparable](arrs ...[]T) []T {
	if len(arrs) == 0 {
		return nil
	}
	counts := make(map[T]int)
	for _, v := range arrs[0] {
		counts[v] = 1
	}
	for i, arr := range arrs[1:] {
		for _, v := range arr {
			if counts[v] == i+1 {
				counts[v]++
			}
		}
	}
	var result []T
	seen := make(map[T]bool)
	for _, v := range arrs[0] {
		if counts[v] == len(arrs) && !seen[v] {
			result = append(result, v)
			seen[v] = true
		}
	}
	return result
}

// Range generates a sequence of numbers.
func Range(args ...int) []int {
	var start, end, step int
	switch len(args) {
	case 1:
		end = args[0]
		step = 1
	case 2:
		start, end = args[0], args[1]
		step = 1
	case 3:
		start, end, step = args[0], args[1], args[2]
	default:
		return nil
	}
	if step == 0 {
		return nil
	}
	var result []int
	if step > 0 {
		for i := start; i < end; i += step {
			result = append(result, i)
		}
	} else {
		for i := start; i > end; i += step {
			result = append(result, i)
		}
	}
	return result
}

// Pick returns a map with only the specified keys.
func Pick[V any](obj map[string]V, keys ...string) map[string]V {
	result := make(map[string]V)
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			result[k] = v
		}
	}
	return result
}

// Omit returns a map without the specified keys.
func Omit[V any](obj map[string]V, keys ...string) map[string]V {
	exclude := make(map[string]bool)
	for _, k := range keys {
		exclude[k] = true
	}
	result := make(map[string]V)
	for k, v := range obj {
		if !exclude[k] {
			result[k] = v
		}
	}
	return result
}

// IsEmpty checks if a value is "empty" (nil, zero-length, zero value).
func IsEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	case bool:
		return !val
	case int:
		return val == 0
	case float64:
		return val == 0
	}
	return false
}

// Clamp clamps a number within the inclusive range.
func Clamp(n, lower, upper float64) float64 {
	if n < lower {
		return lower
	}
	if n > upper {
		return upper
	}
	return n
}

// Capitalize capitalizes the first letter.
func Capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// CamelCase converts a string to camelCase.
func CamelCase(s string) string {
	words := splitWords(s)
	for i, w := range words {
		if i == 0 {
			words[i] = strings.ToLower(w)
		} else {
			words[i] = Capitalize(strings.ToLower(w))
		}
	}
	return strings.Join(words, "")
}

// SnakeCase converts a string to snake_case.
func SnakeCase(s string) string {
	words := splitWords(s)
	for i := range words {
		words[i] = strings.ToLower(words[i])
	}
	return strings.Join(words, "_")
}

// KebabCase converts a string to kebab-case.
func KebabCase(s string) string {
	words := splitWords(s)
	for i := range words {
		words[i] = strings.ToLower(words[i])
	}
	return strings.Join(words, "-")
}

// Truncate truncates a string to the given length, adding "..." if truncated.
func Truncate(s string, length int) string {
	if len(s) <= length {
		return s
	}
	if length <= 3 {
		return s[:length]
	}
	return s[:length-3] + "..."
}

// Debounce returns a function that delays invoking fn until after wait milliseconds.
func Debounce(fn func(), waitMs int) func() {
	var timer *time.Timer
	var mu sync.Mutex
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(time.Duration(waitMs)*time.Millisecond, fn)
	}
}

// Throttle returns a function that only invokes fn at most once per wait milliseconds.
func Throttle(fn func(), waitMs int) func() {
	var lastCall time.Time
	var mu sync.Mutex
	return func() {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if now.Sub(lastCall) >= time.Duration(waitMs)*time.Millisecond {
			lastCall = now
			fn()
		}
	}
}

// splitWords splits a string into words by camelCase boundaries, spaces, hyphens, and underscores.
func splitWords(s string) []string {
	var words []string
	var current []rune
	for i, r := range s {
		if r == ' ' || r == '-' || r == '_' {
			if len(current) > 0 {
				words = append(words, string(current))
				current = nil
			}
		} else if unicode.IsUpper(r) && i > 0 && len(current) > 0 && !unicode.IsUpper(rune(s[i-1])) {
			words = append(words, string(current))
			current = []rune{r}
		} else {
			current = append(current, r)
		}
	}
	if len(current) > 0 {
		words = append(words, string(current))
	}
	return words
}
