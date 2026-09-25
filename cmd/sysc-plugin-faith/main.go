package main

import (
	"os"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	c := v1.NewClient(os.Stdin, os.Stdout)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.faith", Name: "Faith", Version: "0.1.0"})); err != nil {
		os.Exit(1)
	}
	for {
		msg, err := c.Recv()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *v1.HostShutdown:
			return
		case *v1.ViewOpen:
			_ = c.Snapshot(m.ViewID, 1, &v1.Node{Kind: v1.KindRow})
		}
	}
}
