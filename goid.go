package ramune

import "runtime"

// goid returns the current goroutine ID by parsing runtime.Stack output.
func goid() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// Format: "goroutine 123 [..."
	s := buf[:n]
	s = s[len("goroutine "):]
	for i, b := range s {
		if b == ' ' {
			s = s[:i]
			break
		}
	}
	id := int64(0)
	for _, b := range s {
		id = id*10 + int64(b-'0')
	}
	return id
}
