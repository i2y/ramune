// Package hybrid exposes the read-only analysis side of Ramune's
// TS→Go hybrid transpiler.
//
// The picker inside Ramune decides which top-level TypeScript
// declarations are "type-confirmed enough" to be lifted into native
// Go code at compile time. Analyze reports those decisions for a
// single worker source (with its imported helpers) so wrappers and
// platforms built on top of Ramune — notably openworkers — can
// surface extraction coverage without reimplementing tsgo plumbing.
//
// This package does NOT emit Go code; for that, use
// `ramune compile --hybrid`.
package hybrid

import (
	"github.com/i2y/ramune/internal/gotranspiler/composer"
	"github.com/i2y/ramune/internal/gotranspiler/picker"
)

// Kind distinguishes extractable declaration kinds.
type Kind int

const (
	// KindFunction is a top-level function declaration.
	KindFunction Kind = iota + 1
	// KindClass is a top-level class declaration.
	KindClass
	// KindInterface is a top-level interface declaration. Interfaces
	// never extract to runtime Go; they inform type-confirmation but
	// are emitted as comments.
	KindInterface
)

// String returns the lowercase declaration keyword.
func (k Kind) String() string {
	switch k {
	case KindFunction:
		return "function"
	case KindClass:
		return "class"
	case KindInterface:
		return "interface"
	default:
		return "unknown"
	}
}

// Reason explains why a Candidate was rejected by the picker.
// The zero value indicates acceptance.
type Reason struct {
	// Code is a stable, grep-friendly identifier such as
	// "reasonGenericFunc" or "reasonClosureCapture".
	Code string
	// Detail is a human-readable description.
	Detail string
}

// Candidate is one top-level declaration the picker considered.
type Candidate struct {
	// Name is the identifier as declared in TypeScript.
	Name string
	// Kind is Function / Class / Interface.
	Kind Kind
	// Extracted is true when the picker accepted this declaration
	// for extraction into native Go. False means the declaration
	// stays JS-side.
	Extracted bool
	// Reason is populated when Extracted is false.
	Reason Reason
}

// Report aggregates every candidate the picker considered while
// analyzing a worker.
type Report struct {
	// Filename that was analyzed.
	Filename string
	// Candidates in source order, spanning every user file reachable
	// from Filename (the entry plus its direct and transitive
	// imports, excluding node_modules and .d.ts).
	Candidates []Candidate
}

// Stats returns (accepted, rejected, total) candidate counts.
func (r *Report) Stats() (accepted, rejected, total int) {
	total = len(r.Candidates)
	for _, c := range r.Candidates {
		if c.Extracted {
			accepted++
		}
	}
	rejected = total - accepted
	return
}

// ExtractedNames returns the names of accepted candidates in source
// order.
func (r *Report) ExtractedNames() []string {
	out := make([]string, 0, len(r.Candidates))
	for _, c := range r.Candidates {
		if c.Extracted {
			out = append(out, c.Name)
		}
	}
	return out
}

// Analyze runs the picker over the worker source at filename (and its
// user-reachable imports) and returns the aggregated report.
//
// filename must be a path to a .ts / .tsx / .mts / .js / .mjs file;
// the composer walks the import graph internally. Analyze does NOT
// write Go source to disk.
func Analyze(filename string) (*Report, error) {
	res, err := composer.ComposeFile(filename, composer.Options{
		PkgName:          "analyze",
		NativeModuleName: "native:__analyze__",
	})
	if err != nil {
		return nil, err
	}
	return convert(filename, &res.PickerResult), nil
}

func convert(filename string, pr *picker.Result) *Report {
	out := &Report{
		Filename:   filename,
		Candidates: make([]Candidate, len(pr.Candidates)),
	}
	for i, c := range pr.Candidates {
		out.Candidates[i] = Candidate{
			Name:      c.Name,
			Kind:      convertKind(c.Kind),
			Extracted: c.Extracted,
			Reason:    Reason{Code: c.Reason.Code, Detail: c.Reason.Detail},
		}
	}
	return out
}

func convertKind(k picker.Kind) Kind {
	switch k {
	case picker.KindFunction:
		return KindFunction
	case picker.KindClass:
		return KindClass
	case picker.KindInterface:
		return KindInterface
	}
	return 0
}
