package main

import (
	"strings"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func testTree(ids ...string) *v1.Node {
	children := make([]*v1.Node, 0, len(ids))
	for _, id := range ids {
		children = append(children, &v1.Node{Kind: v1.KindButton, ID: id, Text: "label for " + id})
	}
	return &v1.Node{Kind: v1.KindList, Children: children}
}

func TestActionSignatureCoversAddressedNodesInOrder(t *testing.T) {
	ordered := actionSignature(testTree("one", "two"))
	shuffled := actionSignature(testTree("two", "one"))
	if ordered == shuffled {
		t.Fatalf("signature ignored node order: %q", ordered)
	}
	for _, id := range []string{"one", "two"} {
		if !strings.Contains(ordered, id) {
			t.Fatalf("signature misses %q: %q", id, ordered)
		}
	}
	if !strings.Contains(ordered, "label for two") {
		t.Fatalf("signature misses the node label: %q", ordered)
	}
}

func TestActionSignatureIgnoresUnaddressedStatusLines(t *testing.T) {
	before := testTree("select:c001")
	before.Children = append(before.Children,
		&v1.Node{Kind: v1.KindText, Text: "Refreshing…"})

	after := testTree("select:c001")
	after.Children = append(after.Children,
		&v1.Node{Kind: v1.KindText, Text: "Docker daemon not running"})

	if actionSignature(before) != actionSignature(after) {
		t.Fatal("a status-line swap changed the signature")
	}
}

func TestActionSignatureFollowsAddressedChange(t *testing.T) {
	page := actionSignature(testTree("select:c001", "page:next"))
	nextPage := actionSignature(testTree("select:c243", "page:next"))
	if page == nextPage {
		t.Fatal("a page change did not move the signature")
	}
	off := testTree("select:c001", "page:next")
	off.Children[1].Disabled = true
	if page == actionSignature(off) {
		t.Fatal("a disabled control did not move the signature")
	}
}

func TestActionSignatureOfEmptyTreeIsEmpty(t *testing.T) {
	if got := actionSignature(nil); got != "" {
		t.Fatalf("nil tree signature = %q, want empty", got)
	}
	if got := actionSignature(&v1.Node{Kind: v1.KindColumn}); got != "" {
		t.Fatalf("signature of a tree without ids = %q, want empty", got)
	}
}
