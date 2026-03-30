// Package zod provides a Go adapter for the npm "zod" schema validation library.
package zod

import (
	"fmt"
	"regexp"
)

// ParseResult holds the result of a Parse or SafeParse operation.
type ParseResult struct {
	Success bool
	Data    any
	Error   *ZodError
}

// ZodError represents a validation error.
type ZodError struct {
	Issues []ZodIssue
}

func (e *ZodError) Error() string {
	if len(e.Issues) == 0 {
		return "validation error"
	}
	return e.Issues[0].Message
}

// ZodIssue represents a single validation issue.
type ZodIssue struct {
	Path    []string
	Message string
	Code    string
}

// Schema is the interface all zod schemas implement.
type Schema interface {
	Parse(v any) (any, error)
	SafeParse(v any) ParseResult
}

// --- String Schema ---

type StringSchema struct {
	minLen   *int
	maxLen   *int
	pattern  *regexp.Regexp
	email    bool
	url      bool
	optional bool
	defVal   *string
}

func String() *StringSchema { return &StringSchema{} }

func (s *StringSchema) Min(n int) *StringSchema              { s.minLen = &n; return s }
func (s *StringSchema) Max(n int) *StringSchema              { s.maxLen = &n; return s }
func (s *StringSchema) Email() *StringSchema                 { s.email = true; return s }
func (s *StringSchema) Url() *StringSchema                   { s.url = true; return s }
func (s *StringSchema) Regex(r *regexp.Regexp) *StringSchema { s.pattern = r; return s }
func (s *StringSchema) Optional() *StringSchema              { s.optional = true; return s }
func (s *StringSchema) Default(v string) *StringSchema       { s.defVal = &v; return s }

func (s *StringSchema) Parse(v any) (any, error) {
	if v == nil {
		if s.optional {
			if s.defVal != nil {
				return *s.defVal, nil
			}
			return nil, nil
		}
		return nil, &ZodError{Issues: []ZodIssue{{Message: "required", Code: "invalid_type"}}}
	}
	str, ok := v.(string)
	if !ok {
		return nil, &ZodError{Issues: []ZodIssue{{Message: "expected string", Code: "invalid_type"}}}
	}
	if s.minLen != nil && len(str) < *s.minLen {
		return nil, &ZodError{Issues: []ZodIssue{{Message: fmt.Sprintf("string must be at least %d characters", *s.minLen), Code: "too_small"}}}
	}
	if s.maxLen != nil && len(str) > *s.maxLen {
		return nil, &ZodError{Issues: []ZodIssue{{Message: fmt.Sprintf("string must be at most %d characters", *s.maxLen), Code: "too_big"}}}
	}
	if s.email && !isEmail(str) {
		return nil, &ZodError{Issues: []ZodIssue{{Message: "invalid email", Code: "invalid_string"}}}
	}
	if s.pattern != nil && !s.pattern.MatchString(str) {
		return nil, &ZodError{Issues: []ZodIssue{{Message: "invalid format", Code: "invalid_string"}}}
	}
	return str, nil
}

func (s *StringSchema) SafeParse(v any) ParseResult {
	data, err := s.Parse(v)
	if err != nil {
		return ParseResult{Success: false, Error: err.(*ZodError)}
	}
	return ParseResult{Success: true, Data: data}
}

// --- Number Schema ---

type NumberSchema struct {
	min      *float64
	max      *float64
	int_     bool
	optional bool
	defVal   *float64
}

func Number() *NumberSchema { return &NumberSchema{} }

func (s *NumberSchema) Min(n float64) *NumberSchema     { s.min = &n; return s }
func (s *NumberSchema) Max(n float64) *NumberSchema     { s.max = &n; return s }
func (s *NumberSchema) Int() *NumberSchema              { s.int_ = true; return s }
func (s *NumberSchema) Optional() *NumberSchema         { s.optional = true; return s }
func (s *NumberSchema) Default(v float64) *NumberSchema { s.defVal = &v; return s }

func (s *NumberSchema) Parse(v any) (any, error) {
	if v == nil {
		if s.optional {
			if s.defVal != nil {
				return *s.defVal, nil
			}
			return nil, nil
		}
		return nil, &ZodError{Issues: []ZodIssue{{Message: "required", Code: "invalid_type"}}}
	}
	var num float64
	switch n := v.(type) {
	case float64:
		num = n
	case int:
		num = float64(n)
	default:
		return nil, &ZodError{Issues: []ZodIssue{{Message: "expected number", Code: "invalid_type"}}}
	}
	if s.min != nil && num < *s.min {
		return nil, &ZodError{Issues: []ZodIssue{{Message: fmt.Sprintf("number must be >= %g", *s.min), Code: "too_small"}}}
	}
	if s.max != nil && num > *s.max {
		return nil, &ZodError{Issues: []ZodIssue{{Message: fmt.Sprintf("number must be <= %g", *s.max), Code: "too_big"}}}
	}
	if s.int_ && num != float64(int(num)) {
		return nil, &ZodError{Issues: []ZodIssue{{Message: "expected integer", Code: "invalid_type"}}}
	}
	return num, nil
}

func (s *NumberSchema) SafeParse(v any) ParseResult {
	data, err := s.Parse(v)
	if err != nil {
		return ParseResult{Success: false, Error: err.(*ZodError)}
	}
	return ParseResult{Success: true, Data: data}
}

// --- Boolean Schema ---

type BooleanSchema struct {
	optional bool
}

func Boolean() *BooleanSchema { return &BooleanSchema{} }

func (s *BooleanSchema) Optional() *BooleanSchema { s.optional = true; return s }

func (s *BooleanSchema) Parse(v any) (any, error) {
	if v == nil {
		if s.optional {
			return nil, nil
		}
		return nil, &ZodError{Issues: []ZodIssue{{Message: "required", Code: "invalid_type"}}}
	}
	b, ok := v.(bool)
	if !ok {
		return nil, &ZodError{Issues: []ZodIssue{{Message: "expected boolean", Code: "invalid_type"}}}
	}
	return b, nil
}

func (s *BooleanSchema) SafeParse(v any) ParseResult {
	data, err := s.Parse(v)
	if err != nil {
		return ParseResult{Success: false, Error: err.(*ZodError)}
	}
	return ParseResult{Success: true, Data: data}
}

// --- Object Schema ---

type ObjectSchema struct {
	shape    map[string]Schema
	optional bool
}

func Object(shape map[string]Schema) *ObjectSchema {
	return &ObjectSchema{shape: shape}
}

func (s *ObjectSchema) Optional() *ObjectSchema { s.optional = true; return s }

func (s *ObjectSchema) Parse(v any) (any, error) {
	if v == nil {
		if s.optional {
			return nil, nil
		}
		return nil, &ZodError{Issues: []ZodIssue{{Message: "required", Code: "invalid_type"}}}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, &ZodError{Issues: []ZodIssue{{Message: "expected object", Code: "invalid_type"}}}
	}
	result := make(map[string]any)
	for key, schema := range s.shape {
		val, err := schema.Parse(obj[key])
		if err != nil {
			zodErr := err.(*ZodError)
			for i := range zodErr.Issues {
				zodErr.Issues[i].Path = append([]string{key}, zodErr.Issues[i].Path...)
			}
			return nil, zodErr
		}
		result[key] = val
	}
	return result, nil
}

func (s *ObjectSchema) SafeParse(v any) ParseResult {
	data, err := s.Parse(v)
	if err != nil {
		return ParseResult{Success: false, Error: err.(*ZodError)}
	}
	return ParseResult{Success: true, Data: data}
}

// --- Array Schema ---

type ArraySchema struct {
	element  Schema
	minLen   *int
	maxLen   *int
	optional bool
}

func Array(element Schema) *ArraySchema {
	return &ArraySchema{element: element}
}

func (s *ArraySchema) Min(n int) *ArraySchema { s.minLen = &n; return s }
func (s *ArraySchema) Max(n int) *ArraySchema { s.maxLen = &n; return s }
func (s *ArraySchema) Optional() *ArraySchema { s.optional = true; return s }

func (s *ArraySchema) Parse(v any) (any, error) {
	if v == nil {
		if s.optional {
			return nil, nil
		}
		return nil, &ZodError{Issues: []ZodIssue{{Message: "required", Code: "invalid_type"}}}
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, &ZodError{Issues: []ZodIssue{{Message: "expected array", Code: "invalid_type"}}}
	}
	if s.minLen != nil && len(arr) < *s.minLen {
		return nil, &ZodError{Issues: []ZodIssue{{Message: fmt.Sprintf("array must have at least %d items", *s.minLen), Code: "too_small"}}}
	}
	if s.maxLen != nil && len(arr) > *s.maxLen {
		return nil, &ZodError{Issues: []ZodIssue{{Message: fmt.Sprintf("array must have at most %d items", *s.maxLen), Code: "too_big"}}}
	}
	result := make([]any, len(arr))
	for i, item := range arr {
		val, err := s.element.Parse(item)
		if err != nil {
			return nil, err
		}
		result[i] = val
	}
	return result, nil
}

func (s *ArraySchema) SafeParse(v any) ParseResult {
	data, err := s.Parse(v)
	if err != nil {
		return ParseResult{Success: false, Error: err.(*ZodError)}
	}
	return ParseResult{Success: true, Data: data}
}

// --- Enum Schema ---

type EnumSchema struct {
	values   []string
	optional bool
}

func Enum(values ...string) *EnumSchema {
	return &EnumSchema{values: values}
}

func (s *EnumSchema) Optional() *EnumSchema { s.optional = true; return s }

func (s *EnumSchema) Parse(v any) (any, error) {
	if v == nil {
		if s.optional {
			return nil, nil
		}
		return nil, &ZodError{Issues: []ZodIssue{{Message: "required", Code: "invalid_type"}}}
	}
	str, ok := v.(string)
	if !ok {
		return nil, &ZodError{Issues: []ZodIssue{{Message: "expected string", Code: "invalid_type"}}}
	}
	for _, val := range s.values {
		if str == val {
			return str, nil
		}
	}
	return nil, &ZodError{Issues: []ZodIssue{{Message: fmt.Sprintf("invalid enum value: %s", str), Code: "invalid_enum_value"}}}
}

func (s *EnumSchema) SafeParse(v any) ParseResult {
	data, err := s.Parse(v)
	if err != nil {
		return ParseResult{Success: false, Error: err.(*ZodError)}
	}
	return ParseResult{Success: true, Data: data}
}

// --- Helpers ---

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func isEmail(s string) bool {
	return emailRegex.MatchString(s)
}
