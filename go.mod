module github.com/Nomadcxx/sysc-plugins

// The module calls x509.Certificate.Verify through kdeconnect's image fetch
// and http.Client.Do through the usage collectors, so it needs a standard
// library without GO-2026-6809 (fixed in go1.26.10) and the net/http and
// mime/multipart flaws fixed in go1.26.9 (GO-2026-6617, -6613, -6612, -6611,
// -6610, -6608). CI reads this directive for the toolchain it installs, so
// the floor here is the floor everywhere.
go 1.26.10

require (
	github.com/HugoSmits86/nativewebp v1.3.0
	// hold: the plugin contract moves with the shell's main and no tag
	// covers the generation the plugins are written against (see sysc-1691)
	github.com/Nomadcxx/sysc-shell v0.2.1-0.20261020002813-6a482a94b688
	github.com/godbus/dbus/v5 v5.2.4
	golang.org/x/image v0.47.0
	golang.org/x/sys v0.49.0
	modernc.org/sqlite v1.61.0
)

require (
	github.com/Nomadcxx/sysc-metrics v0.7.3
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/text v0.43.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
