package moonbit

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-plugins/internal/barwidth"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Layout for the 758x450 panel (manifest-declared). The root carries the
// inset; the host measures the wordmark rows at its mono text role. Depth
// comes from stacked surfaces, as in the shell's system monitor: a summary
// card, a category card holding a sunken well, then the action bar and a
// footer line outside the cards.
const (
	panelInset = 12
	cardPad    = 12
	wellHeight = 172
	controlH   = 36
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

// Bar is the fixed bar pill: the theme-accent Moonbit mark and a state word
// or figure. Node count never changes with phase, so the bar does not jitter.
func Bar(s *State) *v1.Node {
	return BarAtWidth(s, barwidth.StandardWidth)
}

func BarAtWidth(s *State, width int) *v1.Node {
	label, tone := barLabel(s)
	if barwidth.Compact(width) {
		label = ""
	}
	btn := &v1.Node{
		Kind:   v1.KindButton,
		ID:     "bar",
		Name:   "Moonbit",
		Role:   "button",
		Events: []v1.EventKind{v1.EventActivate, v1.EventPointer},
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: "moonbit", Tone: v1.ToneAccent},
		},
	}
	if barwidth.Compact(width) {
		btn.Height, btn.Padding = 32, 2
		btn.Children[0].IconSize = min(24, max(1, width-4))
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
	case PhaseWorking:
		return "…", v1.ToneAccent
	}
	if s.Cache != nil && s.Cache.Bytes > 0 {
		return humanBytes(s.Cache.Bytes), v1.ToneSubtle
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

func cacheOf(s *State) *CacheInfo { return s.Cache }

// Panel renders the phase the TUI would be on, with the wordmark header in
// every state, the same identity the terminal shows.
func Panel(s *State) *v1.Node {
	header := &v1.Node{Kind: v1.KindColumn}
	for _, row := range wordmark {
		header.Children = append(header.Children, &v1.Node{Kind: v1.KindText, Text: row, Size: "mono", Tone: v1.ToneAccent, Bold: true, CenterX: true})
	}
	return &v1.Node{
		Kind:    v1.KindColumn,
		Padding: panelInset,
		Gap:     10,
		Children: []*v1.Node{
			header,
			summaryCard(s),
			bodyCard(s),
			actionBar(s),
			footer(s),
		},
	}
}

// summaryCard is the hero: one figure and one caption per phase, with the
// scan and clean progress bars living here.
func summaryCard(s *State) *v1.Node {
	var hero, caption string
	heroTone := v1.ToneNormal
	var extra *v1.Node
	switch s.Phase {
	case PhaseScanning:
		hero = "Scanning"
		if s.ScanCat != "" {
			hero += " · " + s.ScanCat
		}
		caption = fmt.Sprintf("%d of %d categories · %s files · %s", s.ScanIdx, s.ScanTotal, count(s.ScanFiles), humanBytes(s.ScanBytes))
		extra = &v1.Node{Kind: v1.KindProgress, Key: "scan-prog", Value: s.ScanValue(), Animate: true, Name: "Scan progress", Role: "progressbar"}
	case PhaseReview:
		n := len(s.SelectedStats())
		if len(s.Review) == 0 {
			hero, caption = "Nothing to clean", "This scan found no files Moonbit can remove."
			break
		}
		hero = humanBytes(s.selectedBytes()) + " selected"
		caption = fmt.Sprintf("%s files in %d of %s", count(s.selectedFiles()), n, plural(len(s.Review), "category", "categories"))
	case PhaseConfirm:
		hero, heroTone = "Clean the selected categories?", v1.ToneError
		caption = confirmCaption(s)
	case PhaseCleaning:
		hero = "Cleaning"
		caption = fmt.Sprintf("%s of %s files · %s freed", count(s.CleanDone), count(s.CleanTotal), humanBytes(s.CleanFreed))
		extra = &v1.Node{Kind: v1.KindProgress, Key: "clean-prog", Value: s.CleanValue(), Animate: true, Name: "Clean progress", Role: "progressbar"}
	case PhaseDone:
		hero = "Freed " + humanBytes(s.Freed)
		caption = plural(s.Deleted, "file", "files") + " cleaned"
	case PhaseAuth:
		hero = "Password required"
		caption = "Moonbit runs " + s.AuthFor + " as root, the same as starting it with sudo."
	case PhaseDocker:
		hero, caption = "Docker cleanup", "Reclaim space Docker holds in images, containers, networks, volumes and build cache."
	case PhaseDockerConfirm:
		spec := dockerOps[s.DockerOp]
		hero, heroTone = spec.confirm, v1.ToneError
		caption = "Runs docker " + spec.verb + " as root. What it removes can't be recovered."
	case PhaseSchedule:
		hero, caption = "Schedule", "Daemon mode and the timers can't run together: enabling one stops the other."
	case PhaseWorking:
		hero, caption = s.Working, "This can take a while; the panel updates when it finishes."
		extra = &v1.Node{Kind: v1.KindSpinner, Key: "working"}
	default:
		hero, caption = idleSummary(s)
	}
	children := []*v1.Node{
		{Kind: v1.KindText, Text: hero, Size: "title", Bold: true, Tone: heroTone},
		{Kind: v1.KindText, Text: caption, Tone: v1.ToneSubtle},
	}
	if extra != nil {
		children = append(children, extra)
	}
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: cardPad, Gap: 4, Children: children}
}

func idleSummary(s *State) (string, string) {
	c := cacheOf(s)
	if c == nil || c.Files == 0 {
		return "No scan yet", "Run a scan to see what Moonbit would reclaim."
	}
	caption := fmt.Sprintf("cleanable in %s files · %s", count(c.Files), plural(len(c.Categories), "category", "categories"))
	if t := shortTime(c.ScannedAt); t != "" {
		caption += " · scanned " + t
	}
	return humanBytes(c.Bytes), caption
}

func confirmCaption(s *State) string {
	n, trunc := s.selectedFiles(), s.truncatedFiles()
	what := plural(n, "file", "files") + " deleted"
	if trunc > 0 {
		what = fmt.Sprintf("%s deleted, %s emptied in place", count(n-trunc), plural(trunc, "file", "files"))
	}
	return fmt.Sprintf("%s: %s. This runs as root and can't be undone.", humanBytes(s.selectedBytes()), what)
}

// bodyCard is the second card: the category well for scan and clean phases,
// or the password prompt, the Docker choices, or the schedule.
func bodyCard(s *State) *v1.Node {
	switch s.Phase {
	case PhaseAuth:
		return authCard(s)
	case PhaseDocker, PhaseDockerConfirm:
		return dockerCard(s)
	case PhaseSchedule:
		return scheduleCard(s)
	case PhaseWorking:
		return card(&v1.Node{Kind: v1.KindColumn, Height: wellHeight + 24, Children: []*v1.Node{note("Working as root…")}})
	}
	return categoryCard(s)
}

func card(children ...*v1.Node) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: cardPad, Gap: 8, Children: children}
}

// authCard is the password prompt every root run passes, as `sudo moonbit`
// asks in a terminal. The field is masked and the plugin never echoes it.
func authCard(s *State) *v1.Node {
	field := &v1.Node{
		Kind: v1.KindTextInput, ID: "password", Key: "password", Name: "Password for sudo", Role: "textbox",
		Placeholder: "Password", Masked: true, SubmitOnEnter: true, Reseed: s.AuthReseed,
		Width: 360, Height: 40, Padding: 12, Disabled: s.Authorizing,
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit},
	}
	status := subtle("Your password goes to sudo only; it's asked for every run.")
	switch {
	case s.Authorizing:
		status = subtle("Checking with sudo…")
	case s.AuthErr != "":
		status = errText(s.AuthErr)
	}
	c := card(
		&v1.Node{Kind: v1.KindText, Text: "Password for " + userName(), Size: "label", Tone: v1.ToneSubtle},
		field,
		status,
	)
	c.Height = wellHeight + 30
	c.Stroke, c.StrokeFill = 1, "accent"
	return c
}

// dockerOps are the TUI's two Docker cleanups, keyed by moonbit's op names.
var dockerOps = map[string]struct{ label, detail, confirm, verb string }{
	"images": {"Clean Unused Images", "Removes every image no container uses.", "Clean unused Docker images?", "image prune"},
	"all":    {"Clean All Unused Resources", "Removes unused images, stopped containers, networks, volumes and build cache.", "Clean all unused Docker resources?", "system prune"},
}

// ValidDockerOp reports whether op is one of the TUI's own Docker cleanups.
// The docker screen's node ids are host input, so the set is the gate that
// keeps an unknown op out of the sudo request.
func ValidDockerOp(op string) bool {
	_, ok := dockerOps[op]
	return ok
}

func dockerCard(s *State) *v1.Node {
	var rows []*v1.Node
	for _, op := range []string{"images", "all"} {
		spec := dockerOps[op]
		if s.Phase == PhaseDockerConfirm && op != s.DockerOp {
			continue
		}
		rows = append(rows, &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
			{Kind: v1.KindText, Text: spec.label, Bold: true},
			subtle(spec.detail),
		}})
	}
	c := card(&v1.Node{Kind: v1.KindColumn, Fill: "surface", Shape: "medium", Padding: 12, Gap: 12, Height: wellHeight, Children: rows})
	if s.Phase == PhaseDockerConfirm {
		c.Stroke, c.StrokeFill = 1, "error"
	}
	return c
}

// scheduleCard shows daemon mode and the timers with their switches, as the
// TUI's Schedule screen does.
func scheduleCard(s *State) *v1.Node {
	sc := s.Schedule
	daemon := "Off"
	if sc.DaemonEnabled {
		daemon = "On"
		if !sc.DaemonActive {
			daemon = "On, not running"
		}
	}
	timers := "Off"
	if sc.TimersEnabled() {
		timers = "On"
		if !(sc.ScanTimer && sc.CleanTimer) {
			timers = "Partly on"
		}
	}
	rows := []*v1.Node{
		scheduleRow("daemon", "Daemon mode", "Scans hourly and cleans daily in the background.", daemon, sc.DaemonEnabled),
		scheduleRow("timers", "Scan and clean timers", "Quick scan daily at 2 AM; quick clean Sundays at 3 AM.", timers, sc.TimersEnabled()),
	}
	if !sc.Known {
		rows = append(rows, errText("systemctl isn't available, so the schedule can't be read."))
	} else if sc.DaemonEnabled && sc.TimersEnabled() {
		rows = append(rows, errText("Daemon mode and the timers are both on; they can't run together."))
	}
	return card(&v1.Node{Kind: v1.KindColumn, Fill: "surface", Shape: "medium", Padding: 12, Gap: 12, Height: wellHeight, Children: rows})
}

func scheduleRow(target, title, detail, state string, on bool) *v1.Node {
	verb, fill, icon := "Enable", "accent", "play_arrow"
	if on {
		verb, fill, icon = "Disable", "chip", "stop"
	}
	btn := action("sched:"+target+":"+strings.ToLower(verb), verb, fill, icon)
	btn.Name = verb + " " + strings.ToLower(title)
	return &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
			{Kind: v1.KindText, Text: title + " · " + state, Bold: true},
			subtle(detail),
		}},
		btn,
	}}
}

func userName() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "your account"
}

// categoryCard holds a column-label strip and a sunken well listing the
// phase's categories. Its rim follows the phase, as the TUI's frame does.
func categoryCard(s *State) *v1.Node {
	title, rows := categoryRows(s)
	columns := "Files · Size"
	if s.Phase == PhaseDone && len(s.CleanErrs) > 0 {
		columns = "" // the well lists failed paths, not categories
	}
	strip := &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindText, Text: title, Size: "label", Tone: v1.ToneSubtle},
		{Kind: v1.KindText, Text: columns, Size: "label", Tone: v1.ToneSubtle},
	}}
	well := &v1.Node{Kind: v1.KindList, Fill: "surface", Shape: "medium", Padding: 8, Gap: 2, Height: wellHeight, Events: []v1.EventKind{v1.EventScroll}, Children: rows}
	card := &v1.Node{Kind: v1.KindColumn, Fill: "card", Shape: "card", Padding: cardPad, Gap: 6, Children: []*v1.Node{strip, well}}
	switch s.Phase {
	case PhaseScanning, PhaseReview:
		card.Stroke, card.StrokeFill = 1, "accent"
	case PhaseConfirm, PhaseCleaning:
		card.Stroke, card.StrokeFill = 1, "error"
	}
	return card
}

func categoryRows(s *State) (string, []*v1.Node) {
	switch s.Phase {
	case PhaseScanning:
		return "Scanned so far", statRows(s.ScanCats, "Results appear here as each category finishes.")
	case PhaseReview:
		if len(s.Review) == 0 {
			return "Choose what to clean", []*v1.Node{note("No cleanable files found in this scan.")}
		}
		var rows []*v1.Node
		for _, c := range s.Review {
			rows = append(rows, toggleRow(c, s.Selected[c.Name]))
		}
		return "Choose what to clean", rows
	case PhaseConfirm, PhaseCleaning:
		return "To clean", statRows(s.SelectedStats(), "")
	case PhaseDone:
		if len(s.CleanErrs) == 0 {
			return "Cleaned", statRows(s.SelectedStats(), "")
		}
		var rows []*v1.Node
		for i, e := range s.CleanErrs {
			if i == 50 {
				rows = append(rows, note(fmt.Sprintf("and %s more", count(len(s.CleanErrs)-50))))
				break
			}
			rows = append(rows, &v1.Node{Kind: v1.KindText, Text: e, Tone: v1.ToneSubtle})
		}
		return "Could not clean", rows
	}
	var cats []CategoryStat
	if c := cacheOf(s); c != nil {
		cats = c.Categories
	}
	return "Last scan", statRows(cats, "Nothing scanned yet.")
}

func statRows(cats []CategoryStat, empty string) []*v1.Node {
	if len(cats) == 0 && empty != "" {
		return []*v1.Node{note(empty)}
	}
	var rows []*v1.Node
	for _, c := range cats {
		rows = append(rows, statRow(c))
	}
	return rows
}

func statRow(c CategoryStat) *v1.Node {
	name := c.Name
	if c.Truncate {
		name += " (emptied in place)"
	}
	return &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindText, Text: name},
		figures(c),
	}}
}

// toggleRow is one review checkbox: a check when selected, a plus when not,
// so the state reads without relying on fill alone.
func toggleRow(c CategoryStat, on bool) *v1.Node {
	icon, fill, verb := "add", "chip", "Select "
	if on {
		icon, fill, verb = "check", "accent", "Deselect "
	}
	btn := &v1.Node{
		Kind: v1.KindButton, ID: "toggle:" + c.Name, Name: verb + c.Name, Role: "button",
		Events: []v1.EventKind{v1.EventActivate}, Fill: fill, Shape: "small", Height: 28,
		Width: 52 + 8*utf8.RuneCountInString(c.Name),
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: icon},
			{Kind: v1.KindText, Text: c.Name, Bold: on},
		},
	}
	return &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{btn, figures(c)}}
}

// figures is a row's files and size, held clear of the well's scrollbar.
func figures(c CategoryStat) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{
		{Kind: v1.KindText, Text: fmt.Sprintf("%s · %s", count(c.Files), humanBytes(c.Bytes)), Tone: v1.ToneSubtle, Tabular: true},
		{Kind: v1.KindColumn, Width: 14},
	}}
}

// actionBar keeps secondary actions left and the primary right, the same
// slots in every phase.
func actionBar(s *State) *v1.Node {
	var left, right []*v1.Node
	switch s.Phase {
	case PhaseScanning, PhaseCleaning:
		right = []*v1.Node{action("cancel", "Cancel", "chip", "")}
	case PhaseReview:
		left = []*v1.Node{action("back", "Back", "chip", "chevron_left")}
		if len(s.Review) == 0 {
			right = scanActions()
			break
		}
		all := len(s.SelectedStats()) == len(s.Review)
		left = append(left, action("select_all", map[bool]string{true: "Select None", false: "Select All"}[all], "chip", ""))
		n := len(s.SelectedStats())
		clean := action("to_confirm", fmt.Sprintf("Clean %d Selected", n), "accent", "delete")
		if n == 0 {
			clean.Text, clean.Name, clean.Disabled = "Clean Selected", "Select a category to clean", true
		}
		right = []*v1.Node{clean}
	case PhaseConfirm:
		left = []*v1.Node{action("cancel_op", "Back", "chip", "chevron_left")}
		right = []*v1.Node{action("confirm_clean", "Clean Now", "error", "delete")}
	case PhaseDone:
		right = []*v1.Node{action("back", "Done", "accent", "check")}
	case PhaseAuth:
		left = []*v1.Node{action("auth_cancel", "Cancel", "chip", "")}
		run := action("auth_run", "Run as Root", "accent", "lock")
		run.Disabled = s.Authorizing
		right = []*v1.Node{run}
	case PhaseDocker:
		left = []*v1.Node{action("back", "Back", "chip", "chevron_left")}
		right = []*v1.Node{
			action("docker:images", "Unused Images", "chip", ""),
			action("docker:all", "All Unused", "accent", "delete"),
		}
	case PhaseDockerConfirm:
		left = []*v1.Node{action("docker", "Back", "chip", "chevron_left")}
		right = []*v1.Node{action("docker_run", "Clean Now", "error", "delete")}
	case PhaseSchedule:
		left = []*v1.Node{action("back", "Back", "chip", "chevron_left")}
	case PhaseWorking:
		right = []*v1.Node{action("cancel", "Cancel", "chip", "")}
	default:
		left = []*v1.Node{
			action("docker", "Docker", "chip", "dns"),
			action("schedule", "Schedule", "chip", "schedule"),
		}
		right = scanActions()
		if c := cacheOf(s); c != nil && c.Files > 0 {
			right = append([]*v1.Node{action("review_last", "Review", "chip", "check")}, right...)
		}
	}
	return &v1.Node{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
		{Kind: v1.KindRow, Gap: 8, Children: left},
		{Kind: v1.KindRow, Gap: 8, Children: right},
	}}
}

func scanActions() []*v1.Node {
	return []*v1.Node{
		action("scan_deep", "Deep Scan", "chip", "search"),
		action("scan_quick", "Quick Scan", "accent", "bolt"),
	}
}

// footer is the line outside the cards: an error when there is one,
// otherwise what the scan could not reach and when Moonbit last cleaned.
func footer(s *State) *v1.Node {
	if s.Err != "" {
		return errText(s.Err)
	}
	var parts []string
	if n := len(s.ScanSkipped); n > 0 {
		parts = append(parts, plural(n, "category", "categories")+" skipped: read-only to the daemon")
	}
	if n := len(s.ScanErrs); n > 0 {
		parts = append(parts, plural(n, "category", "categories")+" could not be scanned")
	}
	if s.Phase == PhaseDone && len(s.CleanErrs) > 0 {
		parts = append(parts, plural(len(s.CleanErrs), "file", "files")+" could not be cleaned")
	}
	if s.Notice != "" && (s.Phase == PhaseDocker || s.Phase == PhaseSchedule) {
		parts = append(parts, s.Notice)
	}
	if len(parts) == 0 {
		parts = append(parts, scheduleSummary(s.Schedule))
	}
	return subtle(strings.Join(parts, " · "))
}

func scheduleSummary(sc Schedule) string {
	switch {
	case !sc.Known:
		return "Schedule unknown"
	case sc.DaemonEnabled && sc.DaemonActive:
		return "Daemon mode on: scans hourly, cleans daily"
	case sc.DaemonEnabled:
		return "Daemon mode on, but not running"
	case sc.TimersEnabled():
		return "Timers on: quick scan daily, quick clean weekly"
	}
	return "No automatic cleaning scheduled"
}

// action is a padded, fixed-height button; the width is set from the label
// so text never meets the pill edge.
func action(id, label, fill, icon string) *v1.Node {
	w := 32 + 9*utf8.RuneCountInString(label)
	n := &v1.Node{
		Kind: v1.KindButton, ID: id, Name: label, Role: "button",
		Events: []v1.EventKind{v1.EventActivate},
		Fill:   fill, Text: label, Height: controlH, Width: w,
	}
	if icon != "" {
		n.Icon, n.Width = icon, w+24
	}
	return n
}

func note(t string) *v1.Node {
	return &v1.Node{Kind: v1.KindText, Text: t, Tone: v1.ToneSubtle, CenterX: true}
}

func text(t string) *v1.Node    { return &v1.Node{Kind: v1.KindText, Text: t} }
func subtle(t string) *v1.Node  { return &v1.Node{Kind: v1.KindText, Text: t, Tone: v1.ToneSubtle} }
func errText(t string) *v1.Node { return &v1.Node{Kind: v1.KindText, Text: t, Tone: v1.ToneError} }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return count(n) + " " + many
}

// count groups thousands: 12,297.
func count(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// shortTime renders a daemon RFC 3339 stamp as local "15:04", or
// "2 Jan 15:04" when it is not today.
func shortTime(stamp string) string {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || t.IsZero() {
		return ""
	}
	t, now := t.Local(), time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("2 Jan 15:04")
}
