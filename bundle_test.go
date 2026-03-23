package ramune

import (
	"testing"

	"github.com/i2y/ramune/internal/registry"
)

func TestParsePackageSpec(t *testing.T) {
	tests := []struct {
		spec    string
		name    string
		version string
	}{
		{"lodash", "lodash", "*"},
		{"lodash@4", "lodash", "4"},
		{"dayjs@1.11.0", "dayjs", "1.11.0"},
		{"@scope/pkg", "@scope/pkg", "*"},
		{"@scope/pkg@1.2.3", "@scope/pkg", "1.2.3"},
	}
	for _, tt := range tests {
		name, version := registry.ParsePackageSpec(tt.spec)
		if name != tt.name || version != tt.version {
			t.Errorf("ParsePackageSpec(%q) = (%q, %q), want (%q, %q)",
				tt.spec, name, version, tt.name, tt.version)
		}
	}
}

func TestSanitizeVarName(t *testing.T) {
	tests := []struct {
		pkg  string
		want string
	}{
		{"lodash", "lodash"},
		{"my-package", "my_package"},
		{"@scope/pkg", "scope_pkg"},
		{"@my-scope/my-pkg", "my_scope_my_pkg"},
	}
	for _, tt := range tests {
		got := sanitizeVarName(tt.pkg)
		if got != tt.want {
			t.Errorf("sanitizeVarName(%q) = %q, want %q", tt.pkg, got, tt.want)
		}
	}
}

func TestHashPkgsDeterministic(t *testing.T) {
	h1 := hashPkgs([]string{"lodash@4", "dayjs"}, false)
	h2 := hashPkgs([]string{"dayjs", "lodash@4"}, false)
	if h1 != h2 {
		t.Fatalf("hash should be order-independent: %q != %q", h1, h2)
	}

	h3 := hashPkgs([]string{"lodash@4"}, false)
	if h1 == h3 {
		t.Fatal("different deps should produce different hashes")
	}

	h4 := hashPkgs([]string{"lodash@4", "dayjs"}, true)
	if h1 == h4 {
		t.Fatal("nodeCompat should change hash")
	}
}
