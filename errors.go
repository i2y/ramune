package ramune

import (
	"errors"
	"fmt"
	"strings"
)

// ErrJSCNotFound is returned when no suitable JavaScriptCore shared library
// can be located on the system.
var ErrJSCNotFound = errors.New("ramune: shared library not found")

// ErrAlreadyClosed is returned when operations are attempted on a closed Runtime.
var ErrAlreadyClosed = errors.New("ramune: runtime already closed")

// ErrNilValue is returned when an operation is attempted on a nil Value.
var ErrNilValue = errors.New("ramune: operation on nil Value")

// JSError represents an error that originated in the JavaScript runtime.
type JSError struct {
	Context string
	Message string
	Stack   string // JavaScript stack trace, if available
}

func (e *JSError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("ramune: exception during %s", e.Context)
	}
	if e.Stack != "" {
		return fmt.Sprintf("ramune: %s: %s\n%s", e.Context, e.Message, e.Stack)
	}
	return fmt.Sprintf("ramune: %s: %s", e.Context, e.Message)
}

// LibraryNotFoundError provides detailed information about which paths
// were searched when JavaScriptCore could not be found.
type LibraryNotFoundError struct {
	Searched []string
}

func (e *LibraryNotFoundError) Error() string {
	var b strings.Builder
	b.WriteString("ramune: shared library not found\nsearched:\n")
	for _, p := range e.Searched {
		fmt.Fprintf(&b, "  - %s\n", p)
	}
	b.WriteString("hint: on Linux, install libjavascriptcoregtk:  apt install libjavascriptcoregtk-4.0-dev\n")
	return b.String()
}

func (e *LibraryNotFoundError) Unwrap() error {
	return ErrJSCNotFound
}
