package v1

import "regexp"

// MaxPluginIDBytes and MaxEntryIDBytes bound the two identifier kinds. A
// plugin id becomes a state directory name, so it is bounded well inside any
// filesystem's component limit.
const (
	MaxPluginIDBytes = 128
	MaxEntryIDBytes  = 128
)

var (
	// pluginIDPattern is reverse-domain style: lower-case segments joined by
	// single dots. It admits no separator, no dot run, and no leading or
	// trailing dot, so an id can never address a parent directory.
	pluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9][a-z0-9-]*)+$`)
	// entryIDPattern names a widget, panel, service, placement instance, or
	// setting key.
	entryIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// ValidPluginID reports whether s can identify a plugin.
//
// The rule lives beside the wire types because a plugin id is wire identity:
// it appears in the handshake, addresses persistent state, and is written into
// the user's configuration. The manifest validator and the configuration
// loader have to agree on it exactly, and one definition is how they do.
func ValidPluginID(s string) bool {
	return len(s) <= MaxPluginIDBytes && pluginIDPattern.MatchString(s)
}

// ValidEntryID reports whether s can identify an entry within a plugin: a
// widget, panel, or service the manifest declares, a placement instance, or a
// setting key.
func ValidEntryID(s string) bool {
	return len(s) <= MaxEntryIDBytes && entryIDPattern.MatchString(s)
}
