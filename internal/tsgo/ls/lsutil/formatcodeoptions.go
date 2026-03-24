package lsutil

import (
	"strings"

	"github.com/i2y/ramune/internal/tsgo/core"
)

func parseTristate(v any) core.Tristate {
	switch val := v.(type) {
	case bool:
		if val {
			return core.TSTrue
		}
		return core.TSFalse
	case string:
		switch strings.ToLower(val) {
		case "true":
			return core.TSTrue
		case "false":
			return core.TSFalse
		}
	}
	return core.TSUnknown
}

func parseNewLineCharacter(v any) string {
	if s, ok := v.(string); ok {
		switch strings.ToLower(s) {
		case "\r\n", "crlf":
			return "\r\n"
		}
	}
	return "\n"
}

func parseBoolWithDefault(val any, defaultV bool) bool {
	if v, ok := val.(bool); ok {
		return v
	}
	return defaultV
}

func parseIntWithDefault(val any, defaultV int) int {
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return defaultV
}

type IndentStyle int

const (
	IndentStyleNone IndentStyle = iota
	IndentStyleBlock
	IndentStyleSmart
)

func parseIndentStyle(v any) IndentStyle {
	switch s := v.(type) {
	case string:
		switch strings.ToLower(s) {
		case "none":
			return IndentStyleNone
		case "block":
			return IndentStyleBlock
		case "smart":
			return IndentStyleSmart
		}
	}
	return IndentStyleSmart
}

type SemicolonPreference string

const (
	SemicolonPreferenceIgnore SemicolonPreference = "ignore"
	SemicolonPreferenceInsert SemicolonPreference = "insert"
	SemicolonPreferenceRemove SemicolonPreference = "remove"
)

func parseSemicolonPreference(v any) SemicolonPreference {
	if s, ok := v.(string); ok {
		switch strings.ToLower(s) {
		case "ignore":
			return SemicolonPreferenceIgnore
		case "insert":
			return SemicolonPreferenceInsert
		case "remove":
			return SemicolonPreferenceRemove
		}
	}
	return SemicolonPreferenceIgnore
}

type EditorSettings struct {
	BaseIndentSize         int
	IndentSize             int
	TabSize                int
	NewLineCharacter       string
	ConvertTabsToSpaces    bool
	IndentStyle            IndentStyle
	TrimTrailingWhitespace bool
}

type FormatCodeSettings struct {
	EditorSettings
	InsertSpaceAfterCommaDelimiter                              core.Tristate
	InsertSpaceAfterSemicolonInForStatements                    core.Tristate
	InsertSpaceBeforeAndAfterBinaryOperators                    core.Tristate
	InsertSpaceAfterConstructor                                 core.Tristate
	InsertSpaceAfterKeywordsInControlFlowStatements             core.Tristate
	InsertSpaceAfterFunctionKeywordForAnonymousFunctions        core.Tristate
	InsertSpaceAfterOpeningAndBeforeClosingNonemptyParenthesis  core.Tristate
	InsertSpaceAfterOpeningAndBeforeClosingNonemptyBrackets     core.Tristate
	InsertSpaceAfterOpeningAndBeforeClosingNonemptyBraces       core.Tristate
	InsertSpaceAfterOpeningAndBeforeClosingEmptyBraces          core.Tristate
	InsertSpaceAfterOpeningAndBeforeClosingTemplateStringBraces core.Tristate
	InsertSpaceAfterOpeningAndBeforeClosingJsxExpressionBraces  core.Tristate
	InsertSpaceAfterTypeAssertion                               core.Tristate
	InsertSpaceBeforeFunctionParenthesis                        core.Tristate
	PlaceOpenBraceOnNewLineForFunctions                         core.Tristate
	PlaceOpenBraceOnNewLineForControlBlocks                     core.Tristate
	InsertSpaceBeforeTypeAnnotation                             core.Tristate
	IndentMultiLineObjectLiteralBeginningOnBlankLine            core.Tristate
	Semicolons                                                  SemicolonPreference
	IndentSwitchCase                                            core.Tristate
}

func (settings *FormatCodeSettings) ParseEditorSettings(editorSettings map[string]any) *FormatCodeSettings {
	if editorSettings == nil {
		return settings
	}
	for name, value := range editorSettings {
		switch strings.ToLower(name) {
		case "baseindentsize", "indentsize", "tabsize", "newlinecharacter", "converttabstospaces", "indentstyle", "trimtrailingwhitespace":
			settings.Set(name, value)
		}
	}
	return settings
}

func (settings *FormatCodeSettings) Parse(prefs any) bool {
	formatSettingsMap, ok := prefs.(map[string]any)
	formatSettingsParsed := false
	if !ok {
		return false
	}
	for name, value := range formatSettingsMap {
		formatSettingsParsed = settings.Set(name, value) || formatSettingsParsed
	}
	return formatSettingsParsed
}

func (settings *FormatCodeSettings) Set(name string, value any) bool {
	switch strings.ToLower(name) {
	case "baseindentsize":
		settings.BaseIndentSize = parseIntWithDefault(value, 0)
	case "indentsize":
		settings.IndentSize = parseIntWithDefault(value, 4)
	case "tabsize":
		settings.TabSize = parseIntWithDefault(value, 4)
	case "newlinecharacter":
		settings.NewLineCharacter = parseNewLineCharacter(value)
	case "converttabstospaces":
		settings.ConvertTabsToSpaces = parseBoolWithDefault(value, true)
	case "indentstyle":
		settings.IndentStyle = parseIndentStyle(value)
	case "trimtrailingwhitespace":
		settings.TrimTrailingWhitespace = parseBoolWithDefault(value, true)
	case "insertspaceaftercommadelimiter":
		settings.InsertSpaceAfterCommaDelimiter = parseTristate(value)
	case "insertspaceaftersemicoloninformstatements":
		settings.InsertSpaceAfterSemicolonInForStatements = parseTristate(value)
	case "insertspacebeforeandafterbinaryoperators":
		settings.InsertSpaceBeforeAndAfterBinaryOperators = parseTristate(value)
	case "insertspaceafterconstructor":
		settings.InsertSpaceAfterConstructor = parseTristate(value)
	case "insertspaceafterkeywordsincontrolflowstatements":
		settings.InsertSpaceAfterKeywordsInControlFlowStatements = parseTristate(value)
	case "insertspaceafterfunctionkeywordforanonymousfunctions":
		settings.InsertSpaceAfterFunctionKeywordForAnonymousFunctions = parseTristate(value)
	case "insertspaceafteropeningandbeforeclosingnonemptyparenthesis":
		settings.InsertSpaceAfterOpeningAndBeforeClosingNonemptyParenthesis = parseTristate(value)
	case "insertspaceafteropeningandbeforeclosingnonemptybrackets":
		settings.InsertSpaceAfterOpeningAndBeforeClosingNonemptyBrackets = parseTristate(value)
	case "insertspaceafteropeningandbeforeclosingnonemptybraces":
		settings.InsertSpaceAfterOpeningAndBeforeClosingNonemptyBraces = parseTristate(value)
	case "insertspaceafteropeningandbeforeclosingemptybraces":
		settings.InsertSpaceAfterOpeningAndBeforeClosingEmptyBraces = parseTristate(value)
	case "insertspaceafteropeningandbeforeclosingtemplatesttringbraces":
		settings.InsertSpaceAfterOpeningAndBeforeClosingTemplateStringBraces = parseTristate(value)
	case "insertspaceafteropeningandbeforeclosingjsxexpressionbraces":
		settings.InsertSpaceAfterOpeningAndBeforeClosingJsxExpressionBraces = parseTristate(value)
	case "insertspaceaftertypeassertion":
		settings.InsertSpaceAfterTypeAssertion = parseTristate(value)
	case "insertspacebeforefunctionparenthesis":
		settings.InsertSpaceBeforeFunctionParenthesis = parseTristate(value)
	case "placeopenbraceonnewlineforfunctions":
		settings.PlaceOpenBraceOnNewLineForFunctions = parseTristate(value)
	case "placeopenbraceonnewlineforcontrolblocks":
		settings.PlaceOpenBraceOnNewLineForControlBlocks = parseTristate(value)
	case "insertspacebeforetypeannotation":
		settings.InsertSpaceBeforeTypeAnnotation = parseTristate(value)
	case "indentmultilineobjectliteralbeginningonblankline":
		settings.IndentMultiLineObjectLiteralBeginningOnBlankLine = parseTristate(value)
	case "semicolons":
		settings.Semicolons = parseSemicolonPreference(value)
	case "indentswitchcase":
		settings.IndentSwitchCase = parseTristate(value)
	default:
		return false
	}
	return true
}

func (settings *FormatCodeSettings) Copy() *FormatCodeSettings {
	if settings == nil {
		return nil
	}
	copied := *settings
	return &copied
}

func GetDefaultFormatCodeSettings() *FormatCodeSettings {
	return &FormatCodeSettings{
		EditorSettings: EditorSettings{
			IndentSize:             4,
			TabSize:                4,
			NewLineCharacter:       "\n",
			ConvertTabsToSpaces:    true,
			IndentStyle:            IndentStyleSmart,
			TrimTrailingWhitespace: true,
		},
		InsertSpaceAfterConstructor:                                 core.TSFalse,
		InsertSpaceAfterCommaDelimiter:                              core.TSTrue,
		InsertSpaceAfterSemicolonInForStatements:                    core.TSTrue,
		InsertSpaceBeforeAndAfterBinaryOperators:                    core.TSTrue,
		InsertSpaceAfterKeywordsInControlFlowStatements:             core.TSTrue,
		InsertSpaceAfterFunctionKeywordForAnonymousFunctions:        core.TSFalse,
		InsertSpaceAfterOpeningAndBeforeClosingNonemptyParenthesis:  core.TSFalse,
		InsertSpaceAfterOpeningAndBeforeClosingNonemptyBrackets:     core.TSFalse,
		InsertSpaceAfterOpeningAndBeforeClosingNonemptyBraces:       core.TSTrue,
		InsertSpaceAfterOpeningAndBeforeClosingTemplateStringBraces: core.TSFalse,
		InsertSpaceAfterOpeningAndBeforeClosingJsxExpressionBraces:  core.TSFalse,
		InsertSpaceBeforeFunctionParenthesis:                        core.TSFalse,
		PlaceOpenBraceOnNewLineForFunctions:                         core.TSFalse,
		PlaceOpenBraceOnNewLineForControlBlocks:                     core.TSFalse,
		Semicolons:                                                  SemicolonPreferenceIgnore,
		IndentSwitchCase:                                            core.TSTrue,
	}
}
