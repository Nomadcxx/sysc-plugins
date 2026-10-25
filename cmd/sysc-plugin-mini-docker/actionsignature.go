package main

import (
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// actionSignature describes what an input event can address in a view: the
// id, kind, label, disabled state and event declarations of every node that
// carries an id, in tree order. Nodes without an id are decoration the host
// renders but nothing can act on, so they are visited only for their
// children.
//
// A refresh that changes only the status lines keeps the signature. The
// view's revision still climbs, but the revision its content last changed at
// stays where it was, so an action the operator aimed at the tree on screen
// is honoured when it arrives instead of being dropped because a spinner
// replaced the tree in between.
func actionSignature(root *v1.Node) string {
	var b strings.Builder
	var walk func(n *v1.Node)
	walk = func(n *v1.Node) {
		if n == nil {
			return
		}
		if n.ID != "" {
			b.WriteString(n.ID)
			b.WriteByte(0)
			b.WriteString(string(n.Kind))
			b.WriteByte(0)
			b.WriteString(n.Text)
			b.WriteByte(0)
			if n.Disabled {
				b.WriteByte('d')
			}
			for _, event := range n.Events {
				b.WriteByte(0)
				b.WriteString(string(event))
			}
			b.WriteByte('\n')
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(root)
	return b.String()
}
