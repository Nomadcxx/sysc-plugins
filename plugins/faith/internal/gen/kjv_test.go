package main

import (
	"strings"
	"testing"
)

func TestParseKJVChecksBookOrder(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"books":[`)
	for i, n := range kjvNames {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"` + n + `","chapters":[{"chapter":1,"verses":[{"verse":1,"text":"  In the  beginning . "}]}]}`)
	}
	b.WriteString(`]}`)
	got, err := ParseKJV(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 66 || got[65][0].Text != "In the beginning." {
		t.Fatalf("got %d books, first text %q", len(got), got[65][0].Text)
	}
	swapped := strings.Replace(b.String(), `"Genesis"`, `"Exodus"`, 1)
	if _, err := ParseKJV(strings.NewReader(swapped)); err == nil {
		t.Fatal("accepted books out of order")
	}
}
