package transformers

import (
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/ast"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/printer"
)

type modifierVisitor struct {
	Transformer
	AllowedModifiers ast.ModifierFlags
}

func (v *modifierVisitor) visit(node *ast.Node) *ast.Node {
	flags := ast.ModifierToFlag(node.Kind)
	if flags != ast.ModifierFlagsNone && flags&v.AllowedModifiers == 0 {
		return nil
	}
	return node
}

func ExtractModifiers(emitContext *printer.EmitContext, modifiers *ast.ModifierList, allowed ast.ModifierFlags) *ast.ModifierList {
	if modifiers == nil {
		return nil
	}
	tx := modifierVisitor{AllowedModifiers: allowed}
	tx.NewTransformer(tx.visit, emitContext)
	return tx.visitor.VisitModifiers(modifiers)
}
