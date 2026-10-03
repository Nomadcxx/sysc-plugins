// Package barwidth contains the shared breakpoint for first-party bar views.
package barwidth

// StandardWidth is the horizontal bar width.
const StandardWidth = 240

// Compact reports when a bar viewport is too narrow for horizontal labels.
// A zero width is the legacy unspecified size and keeps the standard view.
func Compact(width int) bool { return width > 0 && width < 120 }
