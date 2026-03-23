package registry

import "testing"

func TestParseSemver(t *testing.T) {
	tests := []struct {
		in   string
		want semVersion
	}{
		{"4.17.21", semVersion{4, 17, 21, ""}},
		{"0.1.0", semVersion{0, 1, 0, ""}},
		{"1.0.0-beta.1", semVersion{1, 0, 0, "beta.1"}},
		{"v2.3.4", semVersion{2, 3, 4, ""}},
		{"4", semVersion{4, 0, 0, ""}},
		{"4.17", semVersion{4, 17, 0, ""}},
	}
	for _, tt := range tests {
		v, err := parseSemver(tt.in)
		if err != nil {
			t.Errorf("parseSemver(%q) error: %v", tt.in, err)
			continue
		}
		if v != tt.want {
			t.Errorf("parseSemver(%q) = %v, want %v", tt.in, v, tt.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0-beta", "1.0.0", -1},
		{"1.0.0", "1.0.0-beta", 1},
	}
	for _, tt := range tests {
		a, _ := parseSemver(tt.a)
		b, _ := parseSemver(tt.b)
		got := compareSemver(a, b)
		if got != tt.want {
			t.Errorf("compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMatchSemverRange(t *testing.T) {
	tests := []struct {
		version string
		rng     string
		want    bool
	}{
		{"4.17.21", "*", true},
		{"4.17.21", "", true},
		{"4.17.21", "4.17.21", true},
		{"4.17.20", "4.17.21", false},
		{"4.17.21", "^4.0.0", true},
		{"4.17.21", "^4.17.0", true},
		{"5.0.0", "^4.0.0", false},
		{"3.0.0", "^4.0.0", false},
		{"0.2.0", "^0.1.0", false},
		{"0.1.5", "^0.1.0", true},
		{"4.17.21", "~4.17.0", true},
		{"4.18.0", "~4.17.0", false},
		{"4.17.21", ">=4.0.0", true},
		{"3.0.0", ">=4.0.0", false},
		{"4.17.21", "4", true},
		{"5.0.0", "4", false},
		{"4.17.21", "4.17", true},
		{"4.18.0", "4.17", false},
		{"4.17.21", "4.x", true},
		{"5.0.0", "4.x", false},
		{"1.0.0-beta", "^1.0.0", false},
		{"1.0.0-beta", "1.0.0-beta", true},
	}
	for _, tt := range tests {
		v, _ := parseSemver(tt.version)
		got := matchSemverRange(v, tt.rng)
		if got != tt.want {
			t.Errorf("match(%s, %q) = %v, want %v", tt.version, tt.rng, got, tt.want)
		}
	}
}

func TestBestMatch(t *testing.T) {
	versions := []string{"4.17.19", "4.17.20", "4.17.21", "5.0.0", "3.10.1"}

	got, err := bestMatch(versions, "^4.0.0")
	if err != nil || got != "4.17.21" {
		t.Fatalf("bestMatch ^4.0.0: got %q, err %v", got, err)
	}

	got, err = bestMatch(versions, "~4.17.0")
	if err != nil || got != "4.17.21" {
		t.Fatalf("bestMatch ~4.17.0: got %q, err %v", got, err)
	}

	got, err = bestMatch(versions, "4")
	if err != nil || got != "4.17.21" {
		t.Fatalf("bestMatch 4: got %q, err %v", got, err)
	}

	_, err = bestMatch(versions, "^6.0.0")
	if err == nil {
		t.Fatal("expected error for no match")
	}
}
