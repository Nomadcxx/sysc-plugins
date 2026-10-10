package main

import (
	"testing"
)

func TestNodeActionParsing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   string
		kind string
		arg1 string
		arg2 string
	}{
		{"attach:demo", "attach", "demo", ""},
		{"focus:demo:w1:p1", "focus", "demo", "w1:p1"},
		{"read:x:w1:p1", "read", "x", "w1:p1"},
		{"stop:demo", "stop", "demo", ""},
		{"delete:demo", "delete", "demo", ""},
		{"confirmdelete", "confirmdelete", "", ""},
		{"canceldelete", "canceldelete", "", ""},
		{"newstart", "newstart", "", ""},
		{"open", "open", "", ""},
		{"close", "close", "", ""},
		{"refresh", "refresh", "", ""},
		{"newname", "newname", "", ""},
		{"unknown-anything", "unknown-anything", "", ""},
		{"focus:demo", "focus", "demo", ""},
	}
	for _, c := range cases {
		kind, a1, a2 := nodeAction(c.id)
		if kind != c.kind || a1 != c.arg1 || a2 != c.arg2 {
			t.Errorf("nodeAction(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.id, kind, a1, a2, c.kind, c.arg1, c.arg2)
		}
	}
}
