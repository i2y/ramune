package gotranspiler

import (
	"strings"

	"github.com/i2y/ramune/internal/tsgo/checker"
)

// GoTypeCategory classifies a Go type for emission strategy dispatch.
type GoTypeCategory int

const (
	GoTypePrimitive GoTypeCategory = iota // string, float64, bool, int
	GoTypePointer                         // *ClassName, *web.Response
	GoTypeInterface                       // discriminated union interfaces (Shape)
	GoTypeSlice                           // []T
	GoTypeMap                             // map[K]V
	GoTypeFunc                            // func(params) returnType
	GoTypePromise                         // *promise.Promise[T]
	GoTypeJSObject                        // *jsrt.JSObject (fallback when unmappable)
)

// GoTypeInfo holds the categorized Go type for an expression.
type GoTypeInfo struct {
	Category GoTypeCategory
	GoStr    string // Full Go type string: "[]float64", "*web.Response", etc.
	ElemType string // For Slice: element type; for Map: value type; for Promise: inner type
	KeyType  string // For Map: key type
	Name     string // For Pointer/Interface: the type name without *
}

func (g GoTypeInfo) IsAny() bool { return g.Category == GoTypeJSObject || g.GoStr == "any" }
func (g GoTypeInfo) IsString() bool {
	return (g.Category == GoTypePrimitive && g.GoStr == "string") || g.GoStr == "*string"
}
func (g GoTypeInfo) IsFloat64() bool { return g.Category == GoTypePrimitive && g.GoStr == "float64" }
func (g GoTypeInfo) IsBool() bool    { return g.Category == GoTypePrimitive && g.GoStr == "bool" }
func (g GoTypeInfo) IsInt() bool     { return g.Category == GoTypePrimitive && g.GoStr == "int" }
func (g GoTypeInfo) IsNumeric() bool { return g.IsFloat64() || g.IsInt() }
func (g GoTypeInfo) IsSlice() bool   { return g.Category == GoTypeSlice }
func (g GoTypeInfo) IsMap() bool     { return g.Category == GoTypeMap }
func (g GoTypeInfo) IsPointer() bool { return g.Category == GoTypePointer }
func (g GoTypeInfo) IsPromise() bool { return g.Category == GoTypePromise }

// goTypeInfo converts a checker.Type to a categorized GoTypeInfo.
func (m *typeMapper) goTypeInfo(t *checker.Type) GoTypeInfo {
	goStr := m.goType(t)

	// Resolve type aliases: if goStr is a known alias name, replace with underlying type.
	// This handles cases where the checker returns the alias name (e.g., "BodyData")
	// but the transpiler emitted `type BodyData = map[string]any`.
	if m.typeAliases != nil {
		if underlying, ok := m.typeAliases[goStr]; ok {
			goStr = underlying
		}
	}

	info := GoTypeInfo{GoStr: goStr}

	// Check if the checker type is a function type (even if goStr is "any")
	if goStr == "any" && t != nil && t.Flags()&checker.TypeFlagsObject != 0 {
		sigs := m.checker.GetSignaturesOfType(t, checker.SignatureKindCall)
		if len(sigs) > 0 {
			info.Category = GoTypeFunc
			return info
		}
	}

	// jsbridge.Func is the TinyGo-backend lowering for plain TS callables.
	// Categorize as GoTypeFunc so emitCallExpr's "callee is any" cast path
	// stays inactive on identifiers whose checker type is a callable — the
	// same outcome as the default-backend `*ramune.JSFunc` mapping (which
	// lands in GoTypePointer via the `*` prefix below). Without this
	// branch the dotted name fails isValidGoIdentifier and falls into
	// GoTypeJSObject, which makes IsAny() spuriously true.
	if goStr == "jsbridge.Func" {
		info.Category = GoTypeFunc
		return info
	}

	switch {
	case goStr == "any":
		info.Category = GoTypeJSObject
	case goStr == "string" || goStr == "float64" || goStr == "bool" || goStr == "int":
		info.Category = GoTypePrimitive
	case strings.HasPrefix(goStr, "*promise.Promise["):
		info.Category = GoTypePromise
		info.ElemType = goStr[len("*promise.Promise[") : len(goStr)-1]
	case strings.HasPrefix(goStr, "[]"):
		info.Category = GoTypeSlice
		info.ElemType = goStr[2:]
	case strings.HasPrefix(goStr, "map["):
		info.Category = GoTypeMap
		// Parse map[K]V
		depth := 0
		for i := 4; i < len(goStr); i++ {
			if goStr[i] == '[' {
				depth++
			} else if goStr[i] == ']' {
				if depth == 0 {
					info.KeyType = goStr[4:i]
					info.ElemType = goStr[i+1:]
					break
				}
				depth--
			}
		}
	case strings.HasPrefix(goStr, "func("):
		info.Category = GoTypeFunc
	case strings.HasPrefix(goStr, "*"):
		info.Category = GoTypePointer
		info.Name = goStr[1:]
	default:
		// PascalCase name without prefix — check if it's a union interface or struct
		if t != nil && t.Flags()&checker.TypeFlagsUnion != 0 {
			info.Category = GoTypeInterface
			info.Name = goStr
		} else if isGenericType(goStr) {
			// Generic instantiation like Foo[string, any] → treat as struct (pointer)
			info.Category = GoTypePointer
			bracketIdx := strings.Index(goStr, "[")
			if bracketIdx > 0 {
				info.Name = goStr[:bracketIdx]
			} else {
				info.Name = goStr
			}
		} else if isValidGoIdentifier(goStr) && goStr != "" {
			// Check if this named type is actually a map-like type (has string index signatures)
			if t != nil && t.Flags()&checker.TypeFlagsObject != 0 {
				indexInfos := m.checker.GetIndexInfosOfType(t)
				if len(indexInfos) > 0 {
					keyGoType := m.goType(indexInfos[0].KeyType())
					if keyGoType == "string" {
						valGoType := m.goType(indexInfos[0].ValueType())
						info.Category = GoTypeMap
						info.GoStr = "map[" + keyGoType + "]" + valGoType
						info.KeyType = keyGoType
						info.ElemType = valGoType
						return info
					}
				}
			}
			info.Category = GoTypePointer
			info.Name = goStr
		} else {
			info.Category = GoTypeJSObject
		}
	}
	return info
}

// isGenericType checks if goStr is a generic type instantiation like "Foo[string, any]".
func isGenericType(goStr string) bool {
	bracketIdx := strings.Index(goStr, "[")
	if bracketIdx <= 0 || !strings.HasSuffix(goStr, "]") {
		return false
	}
	return isValidGoIdentifier(goStr[:bracketIdx])
}

// typeMapper converts TypeScript types (from the checker) to Go type strings.
type typeMapper struct {
	checker        *checker.Checker
	importedNames  map[string]string // TS name → Go package alias (set by Transpiler)
	pendingImports map[string]string // Go alias → import path (set by Transpiler)
	resolvedPkgs   map[string]bool   // packages actually used (set by qualifyType)
	typeParams     map[string]bool   // currently in-scope type parameter names (set by Transpiler)
	// typeAliases maps Go type alias names to their underlying types.
	// Populated by emitTypeAliasDeclaration when `type X = Y` is emitted.
	// Used by goTypeInfo to resolve alias names to concrete Go types.
	typeAliases map[string]string
	// typeAliasRenames maps original Go type names to prefixed names for unexported types.
	typeAliasRenames map[string]string
	// knownTypes tracks struct/interface/type names defined in the current compilation unit.
	// Populated from classNames after Pass 1.5.
	knownTypes map[string]bool
	// backend selects the runtime concrete type emitted for TS function-
	// typed parameters; see Backend's docs for what each value implies
	// for transitive deps.
	backend Backend
}

func newTypeMapper(c *checker.Checker) *typeMapper {
	return &typeMapper{checker: c, resolvedPkgs: make(map[string]bool)}
}

// jsFuncTypeName returns the Go type used for plain TS callable params under
// the configured backend.
func (m *typeMapper) jsFuncTypeName() string {
	if m.backend == BackendTinyGo {
		return "jsbridge.Func"
	}
	return "*ramune.JSFunc"
}

// goType converts a checker.Type to a Go type string.
func (m *typeMapper) goType(t *checker.Type) string {
	if t == nil {
		return "any"
	}

	flags := t.Flags()

	// Primitive types
	if flags&checker.TypeFlagsNumberLike != 0 {
		return "float64"
	}
	if flags&checker.TypeFlagsStringLike != 0 {
		return "string"
	}
	if flags&checker.TypeFlagsBooleanLike != 0 {
		return "bool"
	}
	if flags&checker.TypeFlagsVoidLike != 0 {
		return ""
	}
	if flags&checker.TypeFlagsNever != 0 {
		return ""
	}
	if flags&checker.TypeFlagsNull != 0 {
		return "any"
	}
	if flags&checker.TypeFlagsBigIntLike != 0 {
		return "*big.Int"
	}
	if flags&checker.TypeFlagsAny != 0 {
		return "any"
	}
	if flags&checker.TypeFlagsUnknown != 0 {
		return "any"
	}

	// Type parameter (generic T, U, etc.)
	if flags&checker.TypeFlagsTypeParameter != 0 {
		sym := t.Symbol()
		if sym != nil {
			name := sym.Name
			// Only preserve if the type parameter is currently in scope
			if m.typeParams != nil && m.typeParams[name] {
				return goExportedName(name)
			}
		}
		return "any"
	}

	// Union type (T | U)
	if flags&checker.TypeFlagsUnion != 0 {
		return m.goUnionType(t)
	}

	// Object types (arrays, classes, interfaces, etc.)
	if flags&checker.TypeFlagsObject != 0 {
		if m.isPlainCallable(t) {
			return m.jsFuncTypeName()
		}
		return m.goObjectType(t)
	}

	// Conditional type (T extends U ? A : B) — resolve via checker
	if flags&checker.TypeFlagsConditional != 0 {
		if resolved := m.checker.GetBaseConstraintOfType(t); resolved != nil && resolved != t {
			return m.goType(resolved)
		}
	}

	// Indexed access type (T[K]) — resolve via checker
	if flags&checker.TypeFlagsIndexedAccess != 0 {
		if resolved := m.checker.GetBaseConstraintOfType(t); resolved != nil && resolved != t {
			return m.goType(resolved)
		}
	}

	// Substitution type — resolve to base
	if flags&checker.TypeFlagsSubstitution != 0 {
		if resolved := m.checker.GetBaseConstraintOfType(t); resolved != nil && resolved != t {
			return m.goType(resolved)
		}
	}

	// Index type (keyof T) — typically resolves to string
	if flags&checker.TypeFlagsIndex != 0 {
		if resolved := m.checker.GetBaseConstraintOfType(t); resolved != nil && resolved != t {
			return m.goType(resolved)
		}
		return "string"
	}

	// Intersection type (T & U) — use first named type or any
	if flags&checker.TypeFlagsIntersection != 0 {
		return m.goIntersectionType(t)
	}

	// Fallback
	return "any"
}

// goUnionType handles union types like T | null, string | number, etc.
func (m *typeMapper) goUnionType(t *checker.Type) string {
	// If the union has a non-generic type alias, preserve the alias name
	if alias := t.Alias(); alias != nil && alias.Symbol() != nil {
		aliasName := alias.Symbol().Name
		if isValidGoIdentifier(aliasName) && !strings.HasPrefix(aliasName, "__") {
			// Skip generic aliases — they resolve to any and can't be instantiated meaningfully
			isGenericAlias := len(alias.TypeArguments()) > 0
			if !isGenericAlias && m.checker != nil {
				localTPs := m.checker.GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(alias.Symbol())
				isGenericAlias = len(localTPs) > 0
			}
			if !isGenericAlias {
				if mapped := m.mapWellKnownType(aliasName); mapped != "" {
					return mapped
				}
				return m.qualifyTypeName(aliasName)
			}
		}
	}

	union := t.AsUnionType()
	types := union.Types()

	// Check for T | null / T | undefined pattern → *T
	var nonNullable []*checker.Type
	hasNull := false
	for _, u := range types {
		if u.Flags()&checker.TypeFlagsNullable != 0 {
			hasNull = true
		} else {
			nonNullable = append(nonNullable, u)
		}
	}

	if hasNull && len(nonNullable) == 1 {
		inner := m.goType(nonNullable[0])
		if inner == "string" || inner == "float64" || inner == "bool" || inner == "int" {
			return "*" + inner
		}
		// `*T`, `[]T`, `map[K]V`, interfaces (`any`, generic discriminated
		// unions): all already nil-able in Go, return as-is.
		if strings.HasPrefix(inner, "*") || strings.HasPrefix(inner, "[]") || strings.HasPrefix(inner, "map[") || inner == "any" {
			return inner
		}
		// Named struct/interface (`Point`, `Counter`, …) — wrap as
		// pointer so the body walker's `p === null` lowers to a valid
		// `p == nil` comparison and field access keeps working via
		// Go's automatic deref. Without this the param would emit as
		// the bare struct type, which has no untyped-nil identity.
		if isValidGoIdentifier(inner) || isGenericType(inner) {
			return "*" + inner
		}
		return inner
	}

	// Check if all members are enum literals from the same enum → use enum type name
	if len(nonNullable) > 0 {
		allEnumLit := true
		var enumParent string
		for _, u := range nonNullable {
			if u.Flags()&checker.TypeFlagsEnumLiteral == 0 {
				allEnumLit = false
				break
			}
			sym := u.Symbol()
			if sym != nil && sym.Parent != nil {
				name := sym.Parent.Name
				if enumParent == "" {
					enumParent = name
				} else if enumParent != name {
					allEnumLit = false
					break
				}
			}
		}
		if allEnumLit && enumParent != "" {
			return goTypeName(enumParent)
		}
	}

	// Check if all members are the same base type (e.g., "up" | "down" → string)
	if len(nonNullable) > 0 {
		allString := true
		allNumber := true
		allBool := true
		for _, u := range nonNullable {
			f := u.Flags()
			if f&checker.TypeFlagsStringLike == 0 {
				allString = false
			}
			if f&checker.TypeFlagsNumberLike == 0 {
				allNumber = false
			}
			if f&checker.TypeFlagsBooleanLike == 0 {
				allBool = false
			}
		}
		if allString {
			if hasNull {
				return "*string"
			}
			return "string"
		}
		if allNumber {
			if hasNull {
				return "*float64"
			}
			return "float64"
		}
		if allBool {
			return "bool"
		}
	}

	// Check for discriminated union — all members are named object types
	allNamed := len(nonNullable) >= 2
	for _, u := range nonNullable {
		if u.Flags()&checker.TypeFlagsObject == 0 || u.Symbol() == nil {
			allNamed = false
			break
		}
	}
	if allNamed {
		// Use TypeToString to get the type alias name (e.g., "Shape")
		tsName := m.checker.TypeToString(t)
		// TypeToString for union alias returns the alias name if defined
		if tsName != "" && isValidGoIdentifier(tsName) && !strings.Contains(tsName, "|") {
			// Skip generic type aliases — they can't be used bare in Go
			if alias := t.Alias(); alias != nil && alias.Symbol() != nil {
				if len(alias.TypeArguments()) > 0 {
					return "any"
				}
				if localTPs := m.checker.GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(alias.Symbol()); len(localTPs) > 0 {
					return "any"
				}
			}
			// Check if this is a well-known Web API type → map it
			if mapped := m.mapWellKnownType(tsName); mapped != "" {
				return mapped
			}
			return m.qualifyTypeName(tsName)
		}
	}

	// General union → any
	return "any"
}

// goObjectType handles object types (arrays, classes, interfaces).
func (m *typeMapper) goObjectType(t *checker.Type) string {
	objFlags := t.ObjectFlags()

	// Reference types (Array<T>, Map<K,V>, Promise<T>, etc.)
	if objFlags&checker.ObjectFlagsReference != 0 {
		typeArgs := m.checker.GetTypeArguments(t)
		target := t.Target()

		if target != nil {
			targetSym := target.Symbol()
			if targetSym != nil {
				name := targetSym.Name
				switch name {
				case "Array", "ReadonlyArray":
					if len(typeArgs) > 0 {
						return "[]" + m.goType(typeArgs[0])
					}
					return "[]any"
				case "Map":
					if len(typeArgs) >= 2 {
						return "map[" + m.goType(typeArgs[0]) + "]" + m.goType(typeArgs[1])
					}
					return "map[string]any"
				case "Set":
					if len(typeArgs) > 0 {
						return "map[" + m.goType(typeArgs[0]) + "]struct{}"
					}
					return "map[any]struct{}"
				case "Promise":
					if len(typeArgs) > 0 {
						inner := m.goType(typeArgs[0])
						if inner == "" {
							inner = "any"
						}
						return "*promise.Promise[" + inner + "]"
					}
					return "*promise.Promise[any]"
				default:
					// Skip well-known types (Web API, etc.) — they have their own mappings
					if mapped := m.mapWellKnownType(name); mapped != "" {
						return mapped
					}
					// Custom generic class (e.g., HonoRequest<P, I>) → include type args
					goName := m.qualifyTypeName(name)
					// Resolve type parameter constraints for defaults
					var localTPs []*checker.Type
					if target != nil && target.Symbol() != nil {
						localTPs = m.checker.GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(target.Symbol())
					}
					if len(typeArgs) > 0 {
						parts := make([]string, len(typeArgs))
						for i, ta := range typeArgs {
							gt := m.goType(ta)
							if gt == "" || gt == "any" {
								gt = m.constraintDefault(localTPs, i)
							}
							parts[i] = gt
						}
						return goName + "[" + strings.Join(parts, ", ") + "]"
					}
					// No type args but target has type params → use constraint defaults
					if len(localTPs) > 0 {
						parts := make([]string, len(localTPs))
						for i := range localTPs {
							parts[i] = m.constraintDefault(localTPs, i)
						}
						return goName + "[" + strings.Join(parts, ", ") + "]"
					}
				}
			}
		}
	}

	// Tuple types → []any (Go has no tuple type; use a slice for simplicity)
	if objFlags&checker.ObjectFlagsTuple != 0 {
		typeArgs := m.checker.GetTypeArguments(t)
		if len(typeArgs) > 0 {
			// Check if all elements are the same type → use typed slice
			firstType := m.goType(typeArgs[0])
			allSame := true
			for _, ta := range typeArgs[1:] {
				if m.goType(ta) != firstType {
					allSame = false
					break
				}
			}
			if allSame {
				return "[]" + firstType
			}
		}
		return "[]any"
	}

	// Mapped types (Record<K,V>, { [K in keyof T]: V }, etc.) → map[K]V
	if objFlags&checker.ObjectFlagsMapped != 0 {
		indexInfos := m.checker.GetIndexInfosOfType(t)
		if len(indexInfos) > 0 {
			keyType := m.goType(indexInfos[0].KeyType())
			valType := m.goType(indexInfos[0].ValueType())
			return "map[" + keyType + "]" + valType
		}
		return "map[string]any"
	}

	// Check if the named type is a type alias for a mapped type (Record/map).
	// Only apply for types that have index signatures AND are not tuples/arrays/classes.
	// Check if the type resolves to a map via index signatures (handles type aliases like BodyData).
	// Skip tuples and array references which also have numeric index signatures.
	if objFlags&checker.ObjectFlagsTuple == 0 && objFlags&checker.ObjectFlagsReference == 0 {
		indexInfos := m.checker.GetIndexInfosOfType(t)
		if len(indexInfos) > 0 {
			keyType := m.goType(indexInfos[0].KeyType())
			if keyType == "string" { // only string-keyed maps (not numeric array indices)
				valType := m.goType(indexInfos[0].ValueType())
				return "map[" + keyType + "]" + valType
			}
		}
	}

	sym := t.Symbol()
	if sym != nil {
		name := sym.Name
		// Skip internal/anonymous type names (contain non-ASCII or __ prefix)
		if !isValidGoIdentifier(name) || strings.HasPrefix(name, "__") {
			return "any"
		}
		// Map well-known Web API / global types to Go equivalents
		if mapped := m.mapWellKnownType(name); mapped != "" {
			return mapped
		}
		goName := m.qualifyTypeName(name)
		// If the symbol defines type parameters, add default type args using constraints
		if m.checker != nil {
			localTPs := m.checker.GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(sym)
			if len(localTPs) > 0 {
				parts := make([]string, len(localTPs))
				for i := range localTPs {
					parts[i] = m.constraintDefault(localTPs, i)
				}
				return goName + "[" + strings.Join(parts, ", ") + "]"
			}
		}
		return goName
	}

	return "any"
}

// constraintDefault returns the Go type to use as a default for the i-th type parameter.
// If the type parameter has a non-any constraint, return the constraint type; otherwise "any".
func (m *typeMapper) constraintDefault(localTPs []*checker.Type, i int) string {
	if i >= len(localTPs) || m.checker == nil {
		return "any"
	}
	constraint := m.checker.GetBaseConstraintOfType(localTPs[i])
	if constraint == nil || constraint == localTPs[i] {
		return "any"
	}
	gt := m.goType(constraint)
	if gt == "" {
		return "any"
	}
	return gt
}

// qualifyTypeName adds package qualification if the type was imported from another package.
// Note: does NOT resolve the import — that's done lazily by emitIdentifier when the
// qualified name actually appears in code (avoids import cycles from type annotations).
func (m *typeMapper) qualifyTypeName(name string) string {
	goName := goTypeName(name)
	if m.importedNames != nil {
		if pkg, ok := m.importedNames[name]; ok && pkg != "" {
			return pkg + "." + goName
		}
	}
	// Apply file-prefix renames for unexported types
	if m.typeAliasRenames != nil {
		if renamed, ok := m.typeAliasRenames[goName]; ok {
			return renamed
		}
	}
	return goName
}

// goIntersectionType handles intersection types (T & U).
// For primitive & object intersections (e.g., string & {callbacks: ...}):
//   - If the object part has properties → use the named type (struct)
//   - If no named type exists → use the primitive
func (m *typeMapper) goIntersectionType(t *checker.Type) string {
	// Use the type alias name if available (e.g., HtmlEscapedString for string & HtmlEscaped).
	// The alias is preserved by the checker but was previously inaccessible.
	if alias := t.Alias(); alias != nil && alias.Symbol() != nil {
		aliasName := alias.Symbol().Name
		if isValidGoIdentifier(aliasName) && !strings.HasPrefix(aliasName, "__") {
			goName := m.qualifyTypeName(aliasName)
			// Add type arguments if the alias is generic
			if aliasArgs := alias.TypeArguments(); len(aliasArgs) > 0 {
				var localTPs []*checker.Type
				if m.checker != nil {
					localTPs = m.checker.GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(alias.Symbol())
				}
				parts := make([]string, len(aliasArgs))
				for i, ta := range aliasArgs {
					gt := m.goType(ta)
					if gt == "" || gt == "any" {
						gt = m.constraintDefault(localTPs, i)
					}
					parts[i] = gt
				}
				return goName + "[" + strings.Join(parts, ", ") + "]"
			}
			// Check if alias symbol has type params even without args
			if m.checker != nil {
				localTPs := m.checker.GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(alias.Symbol())
				if len(localTPs) > 0 {
					parts := make([]string, len(localTPs))
					for i := range localTPs {
						parts[i] = m.constraintDefault(localTPs, i)
					}
					return goName + "[" + strings.Join(parts, ", ") + "]"
				}
			}
			return goName
		}
	}

	inter := t.AsIntersectionType()
	types := inter.Types()

	hasPrimitive := false
	for _, u := range types {
		if u.Flags()&(checker.TypeFlagsStringLike|checker.TypeFlagsNumberLike|checker.TypeFlagsBooleanLike) != 0 {
			hasPrimitive = true
		}
	}

	// Try named object types first
	for _, u := range types {
		if u.Flags()&checker.TypeFlagsObject != 0 {
			// Check if this object type has a symbol name (named type, not anonymous)
			if u.Symbol() != nil && isValidGoIdentifier(u.Symbol().Name) && !strings.HasPrefix(u.Symbol().Name, "__") {
				symName := u.Symbol().Name
				// Apply well-known type mapping first
				if mapped := m.mapWellKnownType(symName); mapped != "" {
					return mapped
				}
				return m.qualifyTypeName(symName)
			}
			// Try TypeToString for alias names
			name := m.checker.TypeToString(u)
			if name != "" && isValidGoIdentifier(name) && !strings.Contains(name, "{") && !strings.Contains(name, "|") {
				if mapped := m.mapWellKnownType(name); mapped != "" {
					return mapped
				}
				return m.qualifyTypeName(name)
			}
			result := m.goObjectType(u)
			if result != "any" {
				return result
			}
		}
	}

	// Check if the intersection type itself has a named alias
	if t.Symbol() != nil {
		name := t.Symbol().Name
		if isValidGoIdentifier(name) && !strings.HasPrefix(name, "__") {
			return m.qualifyTypeName(name)
		}
	}

	// Fallback to primitive if no named object type found
	if hasPrimitive {
		for _, u := range types {
			if u.Flags()&checker.TypeFlagsStringLike != 0 {
				return "string"
			}
			if u.Flags()&checker.TypeFlagsNumberLike != 0 {
				return "float64"
			}
			if u.Flags()&checker.TypeFlagsBooleanLike != 0 {
				return "bool"
			}
		}
	}
	return "any"
}

// wellKnownTypeMap maps Web API / global type names to their Go equivalents.
// Data-driven: add new mappings here without changing dispatch logic.
var wellKnownTypeMap = map[string]string{
	// Web API types with concrete Go structs
	"Response":    "*web.Response",
	"Request":     "*web.Request",
	"Headers":     "*web.Headers",
	"HeadersInit": "*web.Headers",
	"URL":         "*web.URL",
	"TextEncoder": "*web.TextEncoder",
	"TextDecoder": "*web.TextDecoder",
	"FormData":    "*web.FormData",
	// JS built-in collections
	"Array": "[]any",
	// Error types
	"Error":          "*jsrt.JSError",
	"TypeError":      "*jsrt.JSError",
	"RangeError":     "*jsrt.JSError",
	"SyntaxError":    "*jsrt.JSError",
	"ReferenceError": "*jsrt.JSError",
	// Types mapped to any (no Go equivalent)
	"ReadableStream": "any", "WritableStream": "any", "TransformStream": "any",
	"ReadableStreamDefaultReader": "any", "WritableStreamDefaultWriter": "any",
	"ReadableStreamDefaultController": "any",
	"Uint8Array":                      "any", "ArrayBuffer": "any", "ArrayBufferLike": "any",
	"BufferSource": "any", "ArrayBufferView": "any",
	"Blob": "any", "File": "any", "AbortController": "any", "AbortSignal": "any",
	"URLSearchParams": "any", "CryptoKey": "any", "SubtleCrypto": "any", "RequestInit": "any",
	"Function": "any", "RegExp": "any", "Date": "any", "Symbol": "any", "Proxy": "any",
	"PromiseLike": "any", "IterableIterator": "any", "Iterator": "any",
	"PropertyKey": "any", "SharedArrayBuffer": "any", "DataView": "any",
	"FormDataEntryValue": "any",
}

// mapWellKnownType maps a type name to its Go equivalent if it's a well-known Web API / global type.
// Returns empty string if no mapping exists.
func (m *typeMapper) mapWellKnownType(name string) string {
	if goType, ok := wellKnownTypeMap[name]; ok {
		return goType
	}
	return ""
}

// isPlainCallable reports whether t is a bare function type (anonymous or
// aliased `(args) => R`) — one call signature, no ctor/index/properties.
// Only these lower to *ramune.JSFunc; named types that happen to carry a
// call signature stay on their existing lowering path.
func (m *typeMapper) isPlainCallable(t *checker.Type) bool {
	if m == nil || m.checker == nil || t == nil {
		return false
	}
	if t.Flags()&checker.TypeFlagsObject == 0 {
		return false
	}
	// Array / Promise / Map / Set are object-flagged references — never
	// callable in the plain sense we care about.
	if t.ObjectFlags()&checker.ObjectFlagsReference != 0 {
		return false
	}
	calls := m.checker.GetSignaturesOfType(t, checker.SignatureKindCall)
	if len(calls) == 0 {
		return false
	}
	if len(m.checker.GetSignaturesOfType(t, checker.SignatureKindConstruct)) > 0 {
		return false
	}
	if len(m.checker.GetIndexInfosOfType(t)) > 0 {
		return false
	}
	// Excludes named types that define properties alongside the call
	// signature (e.g., `interface Handler { (x): y; tag: string }`). Go has
	// no natural lowering for those and the picker already rejects them.
	for _, p := range m.checker.GetPropertiesOfType(t) {
		if p == nil {
			continue
		}
		if p.Name != "" && p.Name != "prototype" {
			return false
		}
	}
	return true
}

// isSafeType returns true if the Go type string is guaranteed to compile correctly.
// Safe types: primitives, collections, promises, func types, well-known types, pkg-qualified names,
// and types defined in the current compilation unit (knownTypes).
func (m *typeMapper) isSafeType(goStr string) bool {
	switch goStr {
	case "string", "float64", "bool", "int", "any", "", "byte", "error":
		return true
	}
	// Pointer to safe type
	if strings.HasPrefix(goStr, "*") {
		return m.isSafeType(goStr[1:])
	}
	// Slice
	if strings.HasPrefix(goStr, "[]") {
		return m.isSafeType(goStr[2:])
	}
	// Map — always safe (keys/values resolve to any at worst)
	if strings.HasPrefix(goStr, "map[") {
		return true
	}
	// Promise
	if strings.HasPrefix(goStr, "promise.Promise[") {
		return true
	}
	// Func type
	if strings.HasPrefix(goStr, "func(") || strings.HasPrefix(goStr, "func ") {
		return true
	}
	// Package-qualified names (e.g., web.Response, jsrt.JSError)
	if strings.Contains(goStr, ".") {
		return true
	}
	// Generic type: check base name
	if idx := strings.Index(goStr, "["); idx > 0 {
		baseName := goStr[:idx]
		return m.isSafeType(baseName)
	}
	// Well-known type (check both raw name and as value in wellKnownTypeMap)
	if _, ok := wellKnownTypeMap[goStr]; ok {
		return true
	}
	// Type defined in current compilation unit
	if m.knownTypes != nil && m.knownTypes[goStr] {
		return true
	}
	return false
}

// goReturnType returns the Go return type string. Empty string means no return (void).
func (m *typeMapper) goReturnType(t *checker.Type) string {
	if t == nil {
		return ""
	}
	flags := t.Flags()
	if flags&(checker.TypeFlagsVoid|checker.TypeFlagsUndefined) != 0 {
		return ""
	}
	return m.goType(t)
}
