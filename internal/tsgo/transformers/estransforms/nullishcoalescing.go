package estransforms

import (
	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/transformers"
)

type nullishCoalescingTransformer struct {
	transformers.Transformer
}

func (ch *nullishCoalescingTransformer) visit(node *ast.Node) *ast.Node {
	if node.SubtreeFacts()&ast.SubtreeContainsNullishCoalescing == 0 {
		return node
	}
	switch node.Kind {
	case ast.KindBinaryExpression:
		return ch.visitBinaryExpression(node.AsBinaryExpression())
	default:
		return ch.Visitor().VisitEachChild(node)
	}
}

func (ch *nullishCoalescingTransformer) visitBinaryExpression(node *ast.BinaryExpression) *ast.Node {
	switch node.OperatorToken.Kind {
	case ast.KindQuestionQuestionToken:
		left := ch.Visitor().VisitNode(node.Left)
		right := left
		if !transformers.IsSimpleCopiableExpression(left) {
			right = ch.Factory().NewTempVariable()
			ch.EmitContext().AddVariableDeclaration(right)
			left = ch.Factory().NewAssignmentExpression(right, left)
		}
		return ch.Factory().NewConditionalExpression(
			createNotNullCondition(ch.EmitContext(), left, right, false),
			ch.Factory().NewToken(ast.KindQuestionToken),
			right,
			ch.Factory().NewToken(ast.KindColonToken),
			ch.Visitor().VisitNode(node.Right),
		)
	default:
		return ch.Visitor().VisitEachChild(node.AsNode())
	}
}

func newNullishCoalescingTransformer(opts *transformers.TransformOptions) *transformers.Transformer {
	tx := &nullishCoalescingTransformer{}
	return tx.NewTransformer(tx.visit, opts.Context)
}
