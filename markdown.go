package ramune

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
)

func goBunMarkdownHTML(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("markdown: input required")
	}
	src, _ := args[0].(string)
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(src), &buf); err != nil {
		return nil, err
	}
	return buf.String(), nil
}

func (r *Runtime) installMarkdown() error {
	if err := r.registerFuncLocked("__go_bun_markdown_html", goBunMarkdownHTML); err != nil {
		return err
	}
	return r.execLocked(`(function() {
	globalThis.Ramune.markdown = function(src) {
		return __go_bun_markdown_html(src);
	};
	globalThis.Ramune.markdown.html = function(src) {
		return __go_bun_markdown_html(src);
	};
})();`)
}
