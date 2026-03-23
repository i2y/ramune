package ramune_test

import (
	"testing"
)

func TestBunFileReadWrite(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		Bun.write("/tmp/ramune_bun_test.txt", "bun compat test");
		var f = Bun.file("/tmp/ramune_bun_test.txt");
		var result = {
			name: f.name,
			exists: f.exists()
		};
		JSON.stringify(result);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != `{"name":"ramune_bun_test.txt","exists":true}` {
		t.Fatalf("got %s", s)
	}

	r.Exec(`require('fs').rmSync('/tmp/ramune_bun_test.txt')`)
}

func TestBunVersion(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`typeof Bun.serve === 'function' && typeof Bun.file === 'function'`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	b, _ := v.Bool()
	if !b {
		t.Fatal("Bun.serve or Bun.file not available")
	}
}
