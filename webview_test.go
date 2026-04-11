package ramune_test

import (
	"testing"
)

func TestWebViewAPIRegistered(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		JSON.stringify({
			hasConstructor: typeof Ramune.WebView === 'function',
			hasPrototype: typeof Ramune.WebView.prototype === 'object',
			hasNavigate: typeof Ramune.WebView.prototype.navigate === 'function',
			hasEval: typeof Ramune.WebView.prototype.eval === 'function',
			hasSetTitle: typeof Ramune.WebView.prototype.setTitle === 'function',
			hasSetSize: typeof Ramune.WebView.prototype.setSize === 'function',
			hasSetHtml: typeof Ramune.WebView.prototype.setHtml === 'function',
			hasInit: typeof Ramune.WebView.prototype.init === 'function',
			hasDestroy: typeof Ramune.WebView.prototype.destroy === 'function',
			hasOnclose: typeof Ramune.WebView.prototype.onclose === 'function'
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	expected := `{"hasConstructor":true,"hasPrototype":true,"hasNavigate":true,"hasEval":true,"hasSetTitle":true,"hasSetSize":true,"hasSetHtml":true,"hasInit":true,"hasDestroy":true,"hasOnclose":true}`
	if s != expected {
		t.Fatalf("got %s", s)
	}
}
