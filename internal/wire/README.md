# Vendored copy of sysc-shell's `plugin/v1` wire protocol

This directory mirrors `github.com/Nomadcxx/sysc-shell/plugin/v1` (package
name `v1` is kept on purpose so plugin code can switch imports without
renaming identifiers).

## Why vendored?

sysc-shell currently cannot be consumed as a Go module: its tree contains
`internal/platform/wayland/aux.go`, and the Go module proxy refuses to zip
any module containing a path element named `aux` (a reserved Windows device
name). Until that file is renamed upstream, `go get github.com/Nomadcxx/sysc-shell`
fails for every consumer. Once upstream is fixed and tagged, this copy can be
replaced with a normal `require`.

## Maintenance rule

The wire protocol is the compatibility contract between the shell and its
plugins. When `plugin/v1` changes in sysc-shell, re-copy the changed files
here and re-run the test suite; the protocol handshake (major version check)
is the backstop against accidental drift.
