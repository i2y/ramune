// Package picker decides which top-level TypeScript declarations are safe to
// extract as native Go code in the hybrid TS→Go transpile pipeline.
//
// The picker is a pure read-only analysis pass. It receives a parsed source
// file and its type checker, and returns a deterministic ordered list of
// Candidate values marking each top-level FunctionDeclaration (and, in later
// versions, ClassDeclaration) as either Extracted or not, with a Reason.
//
// Non-extractable candidates stay in JS at runtime. This package never writes
// Go source itself — it only classifies.
package picker

import (
	"fmt"
	"io"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// Kind distinguishes extractable declaration kinds.
type Kind int

const (
	KindFunction Kind = iota
	KindClass
)

func (k Kind) String() string {
	switch k {
	case KindFunction:
		return "function"
	case KindClass:
		return "class"
	default:
		return "?"
	}
}

// Reason explains why a Candidate was (not) extracted. Code is a stable,
// grep-friendly identifier; Detail is human-readable context.
type Reason struct {
	Code   string
	Detail string
}

// Candidate is one top-level declaration the picker considered.
type Candidate struct {
	Node      *ast.Node
	Name      string
	Kind      Kind
	Extracted bool
	Reason    Reason
}

// Result is the per-file output of Pick.
type Result struct {
	File       string
	Candidates []Candidate
}

// Options holds tuning knobs. v1 has none; kept here as a forward-compatibility
// seam so callers don't need to change signatures when v1.1+ adds options.
type Options struct{}

// Pick walks the top-level statements of sf and classifies each
// FunctionDeclaration (v1 scope) as extractable or not.
//
// Classes, interfaces, enums, module declarations, and everything else is
// ignored in v1 — those land in JS unchanged.
func Pick(sf *ast.SourceFile, ck *checker.Checker, _ Options) Result {
	r := Result{File: sf.FileName()}
	if sf.Statements == nil {
		return r
	}

	// Pre-collect peer names so IsFunctionExtractable can resolve forward calls.
	topLevelFuncs := map[string]struct{}{}
	for _, stmt := range sf.Statements.Nodes {
		if stmt.Kind != ast.KindFunctionDeclaration {
			continue
		}
		fd := stmt.AsFunctionDeclaration()
		if fd == nil || fd.Name() == nil {
			continue
		}
		topLevelFuncs[fd.Name().AsIdentifier().Text] = struct{}{}
	}

	for _, stmt := range sf.Statements.Nodes {
		if stmt.Kind != ast.KindFunctionDeclaration {
			continue
		}
		fd := stmt.AsFunctionDeclaration()
		if fd == nil || fd.Name() == nil {
			continue
		}
		name := fd.Name().AsIdentifier().Text
		ok, reason := IsFunctionExtractable(stmt, ck, topLevelFuncs)
		r.Candidates = append(r.Candidates, Candidate{
			Node:      stmt,
			Name:      name,
			Kind:      KindFunction,
			Extracted: ok,
			Reason:    reason,
		})
	}
	return r
}

// ExtractedFunctions returns the names of candidates that were extracted,
// in source order.
func (r Result) ExtractedFunctions() []string {
	names := make([]string, 0, len(r.Candidates))
	for _, c := range r.Candidates {
		if c.Extracted && c.Kind == KindFunction {
			names = append(names, c.Name)
		}
	}
	return names
}

// ExtractedNodes returns the AST nodes of extracted candidates in source order.
// Callers pass these directly to TranspileNode.
func (r Result) ExtractedNodes() []*ast.Node {
	nodes := make([]*ast.Node, 0, len(r.Candidates))
	for _, c := range r.Candidates {
		if c.Extracted {
			nodes = append(nodes, c.Node)
		}
	}
	return nodes
}

// Format writes a human-readable report of this Result to w.
// One header line per file, one line per candidate (status, kind, name, reason).
func (r Result) Format(w io.Writer) {
	fmt.Fprintf(w, "picker: %s\n", r.File)
	var ext, skip int
	for _, c := range r.Candidates {
		if c.Extracted {
			ext++
			fmt.Fprintf(w, "  extracted  %s %s\n", c.Kind, c.Name)
			continue
		}
		skip++
		detail := ""
		if c.Reason.Detail != "" {
			detail = "  " + c.Reason.Detail
		}
		fmt.Fprintf(w, "  skipped    %s %s  [%s]%s\n", c.Kind, c.Name, c.Reason.Code, detail)
	}
	fmt.Fprintf(w, "  %d extracted, %d skipped\n", ext, skip)
}
