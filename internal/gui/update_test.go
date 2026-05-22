package gui

import "testing"

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.1.0-alpha", "v0.2.0-alpha", true},
		{"v0.2.0-alpha", "v0.2.0-alpha", false},
		{"v0.2.0-alpha", "v0.1.0-alpha", false},
		{"dev", "v0.1.0-alpha", true},
		{"v0.1.0-alpha", "dev", false},
		{"dev", "dev", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
