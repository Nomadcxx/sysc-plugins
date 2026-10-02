package moonbit

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Layout budget for the 758x450 panel (manifest-declared). The root carries
// the inset; the host measures the wordmark rows at its mono text role.
const (
	panelInset     = 12
	bodyHeight     = 248
	bodyListHeight = 240
)

// wordmark is moonbit's TUI mark (ascii.txt), drawn as accent text because a
// plugin image cannot take the theme colour. The art is cut for a
// left-aligned terminal, so its rows differ in length; each row is padded to
// the widest so centring moves them together and the strokes still meet.
var wordmark = padRows(
	`█▀▄▀█ ▄▀▀▀▄ ▄▀▀▀▄ █▄  █ █▀▀▀▄ ▀▀█▀▀ ▀▀█▀▀    ▄▀    ▄▀`,
	`█   █ █   █ █   █ █ ▀▄█ █▀▀▀▄   █     █    ▄▀    ▄▀`,
	`▀   ▀  ▀▀▀   ▀▀▀  ▀   ▀ ▀▀▀▀  ▀▀▀▀▀   ▀   ▀     ▀`,
)

func padRows(rows ...string) []string {
	w := 0
	for _, r := range rows {
		w = max(w, utf8.RuneCountInString(r))
	}
	for i, r := range rows {
		rows[i] = r + strings.Repeat(" ", w-utf8.RuneCountInString(r))
	}
	return rows
}

// Bar is the fixed bar pill: the catalogue house mark and a state word or
// figure. Node count never changes with phase, so the bar does not jitter.
func Bar(s *State) *v1.Node {
	label, tone := barLabel(s)
	btn := &v1.Node{
		Kind:   v1.KindButton,
		ID:     "bar",
		Name:   "Moonbit",
		Role:   "button",
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: "home", Tone: tone},
		},
	}
	if label != "" {
		btn.Children = append(btn.Children, &v1.Node{Kind: v1.KindText, Text: label, Tone: tone})
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{btn}}
}

func barLabel(s *State) (string, v1.Tone) {
	switch s.Phase {
	case PhaseScanning:
		return fmt.Sprintf("%d%%", int(s.ScanValue()*100)), v1.ToneAccent
	case PhaseCleaning:
		return "clean…", v1.ToneAccent
	case PhaseReview, PhaseConfirm:
		return "review", v1.ToneNormal
	case PhaseDone:
		return "done", v1.ToneNormal
	case PhaseError:
		return "!", v1.ToneError
	}
	if s.Status != nil && s.Status.Cache != nil && s.Status.Cache.Bytes > 0 {
		return humanBytes(s.Status.Cache.Bytes), v1.ToneSubtle
	}
	return "", v1.ToneSubtle
}

// Tooltip is the hover strip: one line of state, one of totals.
func Tooltip(s *State) *v1.Node {
	rows := []*v1.Node{{Kind: v1.KindText, Text: "Moonbit", Bold: true}}
	switch s.Phase {
	case PhaseScanning:
		rows = append(rows, text("Scanning "+s.ScanCat))
	case PhaseCleaning:
		rows = append(rows, text(fmt.Sprintf("Cleaning %d/%d", s.CleanDone, s.CleanTotal)))
	case PhaseError:
		rows = append(rows, errText(s.Err))
	default:
		if c := cacheOf(s); c != nil {
			rows = append(rows, text(fmt.Sprintf("%s in %d files", humanBytes(c.Bytes), c.Files)))
		} else {
			rows = append(rows, subtle("No scan yet"))
		}
	}
	rows = append(rows, subtle("Click to open"))
	return &v1.Node{Kind: v1.KindColumn, Padding: 8, Gap: 4, Children: rows}
}

func cacheOf(s *State) *CacheInfo {
	if s.Status == nil {
		return nil
	}
	return s.Status.Cache
}

// Panel renders the phase the TUI would be on, with the wordmark header in
// every state — the same identity the terminal shows.
func Panel(s *State) *v1.Node {
	header := &v1.Node{Kind: v1.KindColumn}
	for _, row := range wordmark {
		header.Children = append(header.Children, &v1.Node{Kind: v1.KindText, Text: row, Size: "mono", Tone: v1.ToneAccent, Bold: true, CenterX: true})
	}
	body := []*v1.Node{header}
	if s.Phase == PhaseError {
		body = append(body, errText(s.Err))
	}
	switch s.Phase {
	case PhaseScanning:
		body = append(body, scanBody(s)...)
	case PhaseReview:
		body = append(body, reviewBody(s)...)
	case PhaseConfirm:
		body = append(body, confirmBody(s)...)
	case PhaseCleaning:
		body = append(body, cleanBody(s)...)
	case PhaseDone:
		body = append(body, doneBody(s)...)
	default:
		body = append(body, idleBody(s)...)
	}
	return &v1.Node{
		Kind:     v1.KindColumn,
		Padding:  panelInset,
		Gap:      8,
		Children: body,
	}
}

func idleBody(s *State) []*v1.Node {
	c := cacheOf(s)
	head := subtle("No scan yet — run a scan to see what moonbit would reclaim.")
	if c != nil {
		head = &v1.Node{Kind: v1.KindText, Bold: true,
			Text: fmt.Sprintf("%s cleanable in %d files", humanBytes(c.Bytes), c.Files)}
	}
	rows := []*v1.Node{head}
	var cats []CategoryStat
	if c != nil {
		cats = c.Categories
	}
	list := &v1.Node{Kind: v1.KindList, Height: bodyListHeight, Gap: 2}
	for _, cat := range cats {
		list.Children = append(list.Children, statRow(cat))
	}
	if len(cats) > 0 {
		rows = append(rows, list)
	} else if c == nil && s.Status == nil {
		rows = append(rows, subtle("Start the daemon with --socket to connect it."))
	}
	rows = append(rows, buttonRow(
		action("scan_quick", "Quick Scan", "accent"),
		action("scan_deep", "Deep Scan", "soft"),
	))
	return rows
}

func scanBody(s *State) []*v1.Node {
	head := &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindText, Bold: true, Text: fmt.Sprintf("Scanning (%d/%d) %s", s.ScanIdx, s.ScanTotal, s.ScanCat)},
		{Kind: v1.KindText, Text: humanBytes(s.ScanBytes), Tone: v1.ToneSubtle},
	}}
	prog := &v1.Node{Kind: v1.KindProgress, Key: "scan-prog", Value: s.ScanValue(), Animate: true, Name: "Scan progress", Role: "progressbar"}
	detail := subtle(fmt.Sprintf("%d files so far%s", s.ScanFiles, dirSuffix(s.ScanDir)))
	return []*v1.Node{head, prog, detail, buttonRow(action("cancel", "Cancel", "soft"))}
}

func dirSuffix(dir string) string {
	if dir == "" {
		return ""
	}
	return " · " + filepath.Base(dir)
}

func reviewBody(s *State) []*v1.Node {
	rows := []*v1.Node{{Kind: v1.KindText, Bold: true, Text: "Review — choose what to clean"}}
	list := &v1.Node{Kind: v1.KindList, Height: bodyListHeight, Gap: 2, Events: []v1.EventKind{v1.EventScroll}}
	for _, cat := range s.Review {
		on := s.Selected[cat.Name]
		btn := &v1.Node{
			Kind: v1.KindButton, ID: "toggle:" + cat.Name,
			Name: cat.Name, Role: "button", Events: []v1.EventKind{v1.EventActivate},
			Fill:     map[bool]string{true: "accent", false: "soft"}[on],
			Children: []*v1.Node{{Kind: v1.KindText, Text: cat.Name, Bold: on}},
		}
		if on {
			btn.Children = append([]*v1.Node{{Kind: v1.KindIcon, Icon: "check"}}, btn.Children...)
		}
		list.Children = append(list.Children, &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
			btn,
			{Kind: v1.KindText, Text: fmt.Sprintf("%d · %s", cat.Files, humanBytes(cat.Bytes)), Tone: v1.ToneSubtle},
		}})
	}
	n := len(s.SelectedStats())
	rows = append(rows, list, buttonRow(
		action("select_all", "Toggle All", "soft"),
		action("to_confirm", fmt.Sprintf("Clean %d Selected", n), "accent"),
		action("back", "Back", "soft"),
	))
	return rows
}

func confirmBody(s *State) []*v1.Node {
	n, b := s.selectedFiles(), s.selectedBytes()
	rows := []*v1.Node{
		{Kind: v1.KindText, Bold: true, Text: "Clean the selected categories?"},
		text(fmt.Sprintf("%s across %d files in %d categories will be deleted.", humanBytes(b), n, len(s.SelectedStats()))),
		subtle("Deleting runs through the root daemon and cannot be undone."),
	}
	if len(s.ScanErrs) > 0 {
		rows = append(rows, subtle(fmt.Sprintf("%d category errors during the scan", len(s.ScanErrs))))
	}
	rows = append(rows, buttonRow(
		action("cancel_op", "Cancel", "soft"),
		action("confirm_clean", "Clean Now", "error"),
	))
	return rows
}

func cleanBody(s *State) []*v1.Node {
	head := &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindText, Bold: true, Text: fmt.Sprintf("Cleaning (%d/%d)", s.CleanDone, s.CleanTotal)},
		{Kind: v1.KindText, Text: humanBytes(s.CleanFreed), Tone: v1.ToneSubtle},
	}}
	prog := &v1.Node{Kind: v1.KindProgress, Key: "clean-prog", Value: s.CleanValue(), Animate: true, Name: "Clean progress", Role: "progressbar"}
	file := s.CleanFile
	if len(file) > 60 {
		file = "…" + file[len(file)-59:]
	}
	return []*v1.Node{head, prog, subtle(file), buttonRow(action("cancel", "Cancel", "soft"))}
}

func doneBody(s *State) []*v1.Node {
	rows := []*v1.Node{
		{Kind: v1.KindText, Bold: true, Text: "Freed " + humanBytes(s.Freed)},
		text(fmt.Sprintf("%d files deleted", s.Deleted)),
	}
	if len(s.CleanErrs) > 0 {
		rows = append(rows, errText(fmt.Sprintf("%d files could not be deleted", len(s.CleanErrs))))
	}
	rows = append(rows, buttonRow(action("back", "Back", "accent")))
	return rows
}

func statRow(c CategoryStat) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindText, Text: c.Name},
		{Kind: v1.KindText, Text: fmt.Sprintf("%d · %s", c.Files, humanBytes(c.Bytes)), Tone: v1.ToneSubtle},
	}}
}

func buttonRow(btns ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Gap: 8, Children: btns}
}

func action(id, label, fill string) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Name: label, Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
		Fill:   fill, Text: label,
	}
}

func text(t string) *v1.Node    { return &v1.Node{Kind: v1.KindText, Text: t} }
func subtle(t string) *v1.Node  { return &v1.Node{Kind: v1.KindText, Text: t, Tone: v1.ToneSubtle} }
func errText(t string) *v1.Node { return &v1.Node{Kind: v1.KindText, Text: t, Tone: v1.ToneError} }
