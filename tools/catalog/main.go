// Command catalog packages plugin release archives and maintains
// catalog.json, the file sysc-shell's built-in "sysc" plugin source reads.
//
// It has three verbs:
//
//	catalog package  -plugin <dir> -arch <amd64|arm64> -out <dist>
//	catalog update   -tag <dir>-v<version> -dist <dir> [-now RFC3339]
//	catalog validate [-community] [-fetch]
//
// Every verb runs from the sysc-plugins repository root: paths like
// plugins/<dir>, catalog.json and catalog-meta.json are read relative to the
// current directory.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	verb := os.Args[1]
	args := os.Args[2:]

	var err error
	switch verb {
	case "package":
		err = runPackage(args)
	case "update":
		err = runUpdate(args)
	case "validate":
		err = runValidate(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "catalog: unknown verb %q\n", verb)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalog:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: catalog <verb> [flags]

verbs:
  package  -plugin <dir> -arch <amd64|arm64> -out <dist>
  update   -tag <dir>-v<version> -dist <dir> [-now RFC3339]
  validate [-community] [-fetch]`)
}
