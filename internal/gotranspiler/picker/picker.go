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
	KindInterface
	KindConst
)

func (k Kind) String() string {
	switch k {
	case KindFunction:
		return "function"
	case KindClass:
		return "class"
	case KindInterface:
		return "interface"
	case KindConst:
		return "const"
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

// Registry bundles the symbol tables threaded through every body
// walker: same-file functions, cross-file static methods, top-level
// consts. Pick / IsFunctionExtractable / IsClassExtractable all take
// one of these instead of three separate map args, so adding new
// symbol kinds (type aliases, enums) doesn't change every signature.
// Maps inside are mutated by the picker as it accepts candidates;
// callers can construct one ahead of time to thread across files.
type Registry struct {
	Funcs   map[string]struct{}
	Statics map[string]map[string]bool
	Consts  map[string]struct{}
}

// Options holds tuning knobs for Pick.
type Options struct {
	// TopLevelFuncs, if non-nil, is used instead of scanning sf for peer
	// function names. Multi-file callers pass a union across all user
	// source files so cross-file calls to other extractable functions are
	// accepted. When nil (single-file default), Pick collects the set from
	// sf's own top-level.
	TopLevelFuncs map[string]struct{}
	// StaticMethods, if non-nil, is mutated in-place: each accepted class
	// writes its static method names back keyed under the class name.
	// Multi-file callers can share one map across Pick calls so a static
	// method declared in one file is callable from another.
	StaticMethods map[string]map[string]bool
	// TopLevelConsts, if non-nil, is the set of top-level `const` names
	// that have been validated as extractable. Body walkers accept refs
	// to these. Multi-file callers thread the union across files so
	// view() in app.ts can reference a constant declared in palette.ts.
	// When nil (single-file default), Pick collects from sf.
	TopLevelConsts map[string]struct{}
}

// Pick walks the top-level statements of sf and classifies each
// FunctionDeclaration / ClassDeclaration as extractable or not.
//
// Interfaces, enums, module declarations, and everything else is ignored —
// those land in JS unchanged.
func Pick(sf *ast.SourceFile, ck *checker.Checker, opts Options) Result {
	r := Result{File: sf.FileName()}
	if sf.Statements == nil {
		return r
	}

	reg := &Registry{Funcs: opts.TopLevelFuncs, Statics: opts.StaticMethods, Consts: opts.TopLevelConsts}
	if reg.Funcs == nil {
		// Pre-collect peer names so IsFunctionExtractable can resolve forward calls.
		reg.Funcs = map[string]struct{}{}
		for _, stmt := range sf.Statements.Nodes {
			if stmt.Kind != ast.KindFunctionDeclaration {
				continue
			}
			fd := stmt.AsFunctionDeclaration()
			if fd == nil || fd.Name() == nil {
				continue
			}
			reg.Funcs[fd.Name().AsIdentifier().Text] = struct{}{}
		}
	}
	if reg.Statics == nil {
		reg.Statics = map[string]map[string]bool{}
	}
	// Pre-pass: structurally enumerate top-level classes and stamp their
	// static method names into the registry before any body walker runs.
	// Lets a function call `Util.foo(...)` regardless of whether `Util`
	// appears earlier or later in the file (and, when callers share the
	// registry, regardless of which file declares it). The pre-pass is
	// AST-only — no checker calls, no body walks — so the cost is
	// proportional to the top-level declaration count, well under the
	// main validation pass it precedes.
	PreCollectStaticMethods(sf, reg.Statics)

	// Top-level `const` declarations whose initializer is an extractable
	// expression are emitted as Go-side `const` (or `var` when the
	// initializer needs runtime evaluation). Body walkers accept refs to
	// these so views can hoist style constants (ANSI escape strings,
	// theme tokens) without paying a per-frame JSC dispatch.
	if reg.Consts == nil {
		reg.Consts = map[string]struct{}{}
	}
	for _, stmt := range sf.Statements.Nodes {
		if stmt.Kind != ast.KindVariableStatement {
			continue
		}
		vs := stmt.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			continue
		}
		decls := vs.DeclarationList.AsVariableDeclarationList()
		// Reject `let` / `var` — re-assignment would diverge between
		// the JS and Go sides without a sync mechanism. Only `const`
		// (NodeFlagsConst on the declaration list) is sound.
		if decls == nil || decls.Flags&ast.NodeFlagsConst == 0 {
			continue
		}
		// Each VariableStatement may declare multiple names. The
		// emit path goes through the whole statement, so all of them
		// must be acceptable, otherwise the whole declaration is
		// dropped (and bodies that reference any of the names will
		// reject loudly).
		ok := true
		var names []string
		if decls.Declarations != nil {
			for _, decl := range decls.Declarations.Nodes {
				vd := decl.AsVariableDeclaration()
				if vd == nil || vd.Name() == nil || vd.Name().Kind != ast.KindIdentifier {
					ok = false
					break
				}
				if vd.Initializer == nil {
					ok = false
					break
				}
				if r := isExtractableConstInit(ck, vd.Initializer); r != nil {
					ok = false
					break
				}
				names = append(names, vd.Name().AsIdentifier().Text)
			}
		}
		if !ok || len(names) == 0 {
			continue
		}
		for _, n := range names {
			reg.Consts[n] = struct{}{}
		}
		// Emit the const candidate up front so the source-order-preserved
		// extracted-nodes list places it ahead of any function that
		// references it.
		r.Candidates = append(r.Candidates, Candidate{
			Node:      stmt,
			Name:      names[0],
			Kind:      KindConst,
			Extracted: true,
		})
	}

	for _, stmt := range sf.Statements.Nodes {
		switch stmt.Kind {
		case ast.KindFunctionDeclaration:
			fd := stmt.AsFunctionDeclaration()
			if fd == nil || fd.Name() == nil {
				continue
			}
			name := fd.Name().AsIdentifier().Text
			ok, reason := IsFunctionExtractable(stmt, ck, reg)
			r.Candidates = append(r.Candidates, Candidate{
				Node:      stmt,
				Name:      name,
				Kind:      KindFunction,
				Extracted: ok,
				Reason:    reason,
			})
		case ast.KindClassDeclaration:
			id := stmt.Name()
			if id == nil || id.Kind != ast.KindIdentifier {
				continue
			}
			ok, reason := IsClassExtractable(stmt, ck, reg)
			r.Candidates = append(r.Candidates, Candidate{
				Node:      stmt,
				Name:      id.AsIdentifier().Text,
				Kind:      KindClass,
				Extracted: ok,
				Reason:    reason,
			})
		case ast.KindInterfaceDeclaration:
			// Interfaces that satisfy the extractable-object predicate are
			// emitted as Go structs (with JSON tags). Always include them so
			// extracted functions referencing them resolve. Non-qualifying
			// interfaces are simply not emitted; the picker rejects any
			// function whose param/return type points at a non-extractable
			// interface, so unresolved type references can't slip through.
			id := stmt.Name()
			if id == nil || id.Kind != ast.KindIdentifier {
				continue
			}
			declType := ck.GetTypeAtLocation(id)
			if !isExtractableObjectType(ck, declType) {
				continue
			}
			r.Candidates = append(r.Candidates, Candidate{
				Node:      stmt,
				Name:      id.AsIdentifier().Text,
				Kind:      KindInterface,
				Extracted: true,
			})
		}
	}
	return r
}

// PreCollectStaticMethods scans sf's top-level class declarations and
// records their statically-declared method names into registry, keyed
// by class name. Multi-file callers can run this against every source
// file before any Pick call so cross-file `Class.method(...)` resolves
// regardless of declaration order.
//
// The scan is structural — it does not invoke the type checker, walk
// method bodies, or otherwise validate extractability. A class that
// turns out to be rejected during the main Pick pass leaves its names
// in the registry; emit-time the bridge consults the accepted-class
// set, so a `RejectedClass.foo` reference will surface as a Go
// compile error rather than silently emit a missing-symbol call.
func PreCollectStaticMethods(sf *ast.SourceFile, registry map[string]map[string]bool) {
	if sf == nil || sf.Statements == nil || registry == nil {
		return
	}
	for _, stmt := range sf.Statements.Nodes {
		if stmt.Kind != ast.KindClassDeclaration {
			continue
		}
		id := stmt.Name()
		if id == nil || id.Kind != ast.KindIdentifier {
			continue
		}
		cd := stmt.AsClassDeclaration()
		if cd == nil || cd.Members == nil {
			continue
		}
		var statics []string
		for _, member := range cd.Members.Nodes {
			if member.Kind != ast.KindMethodDeclaration {
				continue
			}
			if !ast.HasSyntacticModifier(member, ast.ModifierFlagsStatic) {
				continue
			}
			mname := member.Name()
			if mname == nil || mname.Kind != ast.KindIdentifier {
				continue
			}
			statics = append(statics, mname.AsIdentifier().Text)
		}
		if len(statics) == 0 {
			continue
		}
		className := id.AsIdentifier().Text
		entry := registry[className]
		if entry == nil {
			entry = map[string]bool{}
			registry[className] = entry
		}
		for _, s := range statics {
			entry[s] = true
		}
	}
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

// ExtractedClasses returns the names of class candidates that were extracted,
// in source order. The names are the JS class names (e.g. "Counter").
func (r Result) ExtractedClasses() []string {
	names := make([]string, 0, len(r.Candidates))
	for _, c := range r.Candidates {
		if c.Extracted && c.Kind == KindClass {
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
