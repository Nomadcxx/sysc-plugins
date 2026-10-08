package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/notes"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	pinnedStateKey = "sticky-pinned"
	colorStateBase = "sticky-color."
	// keyStateBase records the shell surface key a note's sticky uses, once
	// a rename has moved it off the default.
	keyStateBase = "sticky-key."
	// A new sticky opens at stickyOrigin and each further one on the same
	// output steps down and right, wrapping after stickyCascade.
	stickyOriginX, stickyOriginY = 56, 92
	stickyStep, stickyCascade    = 28, 8
	stickyWidth, stickyHeight    = 300, 320
)

type view struct {
	kind       v1.ViewKind
	rev        uint64
	name       string
	color      string
	pinned     bool
	output     string
	generation uint32
	// slot is the cascade place a sticky opened at, when this session
	// placed it.
	slot    int
	hasSlot bool
}

type pinnedSurface struct {
	Name   string `json:"name"`
	Output string `json:"output"`
}

// publisher remembers the last tree sent to each view, so the 1s tick only
// sends a view whose content changed.
type publisher map[string][sha256.Size]byte

func (p publisher) changed(id string, tree *v1.Node) bool {
	b, err := json.Marshal(tree)
	if err != nil {
		return true
	}
	sum := sha256.Sum256(b)
	if prev, ok := p[id]; ok && prev == sum {
		return false
	}
	p[id] = sum
	return true
}

func (p publisher) forget(id string) { delete(p, id) }

// cascade is where the next sticky on output opens.
// cascade places a new sticky at the first cascade slot no open sticky on
// output holds, so closing one frees its place; when every slot is taken it
// wraps by count.
func cascade(views map[string]view, output string) (x, y, slot int) {
	used := make([]bool, stickyCascade)
	n := 0
	for _, v := range views {
		if v.kind != v1.ViewFloating || v.output != output {
			continue
		}
		n++
		if v.hasSlot && v.slot >= 0 && v.slot < stickyCascade {
			used[v.slot] = true
		}
	}
	slot = n % stickyCascade
	for i, taken := range used {
		if !taken {
			slot = i
			break
		}
	}
	step := slot * stickyStep
	return stickyOriginX + step, stickyOriginY + step, slot
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	hello, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.notes", Name: "Notes", Version: "1.2.0"}))
	if err != nil {
		return err
	}
	canReadClipboard := slices.Contains(hello.Capabilities, "clipboard-read")
	var sess *notes.Session
	ensure := func() *notes.Session {
		if sess == nil {
			sess = applySettings(nil, nil)
		}
		return sess
	}
	views := map[string]view{}
	pub := publisher{}
	var pins []pinnedSurface
	pinsLoaded := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	ticks := time.NewTicker(time.Second)
	defer ticks.Stop()

	loadPins := func() error {
		if pinsLoaded {
			return nil
		}
		if _, err := getState(ctx, c, pinnedStateKey, &pins); err != nil {
			return err
		}
		pins = normalizePins(pins)
		pinsLoaded = true
		return nil
	}

	snapshot := func() {
		if sess == nil {
			return
		}
		var snap notes.Snapshot
		haveSnap := false
		library := func() notes.Snapshot {
			if !haveSnap {
				snap, haveSnap = sess.Snap(), true
			}
			return snap
		}
		for id, v := range views {
			var tree *v1.Node
			switch v.kind {
			case v1.ViewBar:
				tree = notes.BarTree()
			case v1.ViewTooltip:
				count, last := sess.Stats()
				tree = notes.TooltipTree(count, last, library().Now)
			case v1.ViewFloating:
				doc, err := sess.Document(v.name)
				if err != nil {
					sess.Notify("Could not open sticky note: " + err.Error())
					continue
				}
				tree = notes.StickyTree(doc, v.color)
			default:
				tree = notes.PanelTree(library(), canReadClipboard)
			}
			if !pub.changed(id, tree) {
				continue
			}
			v.rev++
			views[id] = v
			_ = c.Snapshot(id, v.rev, tree)
		}
	}

	editableOpen := func() bool {
		for _, v := range views {
			if v.kind == v1.ViewPanel || v.kind == v1.ViewFloating {
				return true
			}
		}
		return false
	}

	openSticky := func(name, output string, generation uint32) error {
		if output == "" || generation == 0 {
			return errors.New("notes: output identity is unavailable for a sticky note")
		}
		s := ensure()
		doc, err := s.Document(name)
		if err != nil {
			return err
		}
		// A sticky on a blank note is a reason to keep it.
		s.Keep(name)
		if err := loadPins(); err != nil {
			return err
		}
		color := "sun"
		if _, err := getState(ctx, c, colorStateBase+notes.Token(name), &color); err != nil {
			return err
		}
		color = validColor(color)
		x, y, slot := cascade(views, output)
		key, err := stickyKey(ctx, c, name)
		if err != nil {
			return err
		}
		params := v1.SurfaceOpenParams{
			Key: key, Title: notes.DisplayTitle(name, doc.Body),
			Output: output, Generation: generation, X: x, Y: y, Width: stickyWidth, Height: stickyHeight,
		}
		var result v1.SurfaceResult
		if err := call(ctx, c, v1.CallSurfaceOpen, params, &result); err != nil {
			return err
		}
		if result.ViewID == "" {
			return errors.New("notes: shell did not return a sticky view")
		}
		pinned := isPinned(pins, name, output)
		views[result.ViewID] = view{kind: v1.ViewFloating, name: name, color: color, pinned: pinned, output: output, generation: generation, slot: slot, hasSlot: true}
		return call(ctx, c, v1.CallSurfacePin, v1.SurfacePinParams{View: result.ViewID, Pinned: pinned}, nil)
	}

	// afterRename points a renamed note's stickies at the new name, carries
	// its colour and pins over, and reopens the stickies so their title bars
	// follow.
	afterRename := func(oldName, newName string) {
		retargetStickies(views, oldName, newName)
		if err := migrateNoteState(ctx, c, oldName, newName, &pins, loadPins); err != nil {
			sess.Notify("Could not move sticky settings to the new name: " + err.Error())
			return
		}
		var reopen []string
		for id, v := range views {
			if v.kind == v1.ViewFloating && v.name == newName {
				reopen = append(reopen, id)
			}
		}
		for _, id := range reopen {
			v := views[id]
			if err := call(ctx, c, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: id}, nil); err != nil {
				sess.Notify(err.Error())
				continue
			}
			delete(views, id)
			pub.forget(id)
			if err := openSticky(newName, v.output, v.generation); err != nil {
				sess.Notify(err.Error())
			}
		}
	}

	restorePins := func(output string, generation uint32) {
		if output == "" || generation == 0 {
			return
		}
		if err := loadPins(); err != nil {
			ensure().Notify("Could not restore sticky notes: " + err.Error())
			return
		}
		for _, pin := range pins {
			if pin.Output != output {
				continue
			}
			if err := openSticky(pin.Name, output, generation); err != nil {
				ensure().Notify("Could not restore " + pin.Name + ": " + err.Error())
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			if sess != nil {
				_ = sess.Close()
			}
			return nil
		case <-ticks.C:
			if sess != nil && editableOpen() {
				sess.Tick()
				snapshot()
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				if sess != nil {
					_ = sess.Close()
				}
				return nil
			case *v1.ViewOpen:
				ensure()
				v := view{kind: m.View, output: m.Output, generation: m.Generation}
				if old, ok := views[m.ViewID]; ok {
					v.slot, v.hasSlot = old.slot, old.hasSlot
				}
				if m.View == v1.ViewFloating {
					if err := loadPins(); err != nil {
						sess.Notify("Could not load sticky note state: " + err.Error())
					} else {
						for _, pin := range pins {
							if key, err := stickyKey(ctx, c, pin.Name); err == nil && key == m.Entry {
								v.name, v.pinned = pin.Name, isPinned(pins, pin.Name, m.Output)
								break
							}
						}
					}
					if v.name == "" {
						for _, candidate := range views {
							if candidate.kind != v1.ViewFloating {
								continue
							}
							if key, err := stickyKey(ctx, c, candidate.name); err == nil && key == m.Entry {
								v.name, v.color, v.pinned = candidate.name, candidate.color, candidate.pinned
								break
							}
						}
					}
					if v.name == "" {
						sess.Notify("Could not identify a restored sticky note")
						continue
					}
					if v.color == "" {
						v.color = "sun"
						_, _ = getState(ctx, c, colorStateBase+notes.Token(v.name), &v.color)
						v.color = validColor(v.color)
					}
				}
				views[m.ViewID] = v
				pub.forget(m.ViewID)
				snapshot()
				if m.View != v1.ViewFloating {
					restorePins(m.Output, m.Generation)
					snapshot()
				}
			case *v1.ViewClose:
				if v, ok := views[m.ViewID]; ok {
					if v.kind == v1.ViewFloating {
						if err := sess.FlushNote(v.name); err != nil {
							sess.ReportNoteError(v.name, "Save failed while closing sticky: "+err.Error())
						}
					} else if v.kind == v1.ViewPanel {
						if err := sess.Close(); err != nil {
							sess.Notify("Save failed while closing Notes: " + err.Error())
						}
					}
				}
				delete(views, m.ViewID)
				pub.forget(m.ViewID)
				if from, to, ok := sess.TakeRename(); ok {
					afterRename(from, to)
				}
				snapshot()
			case *v1.ViewResync:
				if v, ok := views[m.ViewID]; ok {
					v.rev = 0
					views[m.ViewID] = v
				}
				pub.forget(m.ViewID)
				snapshot()
			case *v1.InputEvent:
				ensure()
				if v, ok := views[m.ViewID]; ok && v.kind == v1.ViewFloating {
					handleSticky(ctx, c, sess, m, v, views, &pins, loadPins)
				} else {
					handlePanel(ctx, c, sess, m, openSticky, views, &pins, loadPins)
				}
				if from, to, ok := sess.TakeRename(); ok {
					afterRename(from, to)
				}
				snapshot()
			case *v1.SettingsChanged:
				sess = applySettings(sess, m.Values)
				snapshot()
			}
		}
	}
}

func handlePanel(ctx context.Context, c *v1.Client, sess *notes.Session, m *v1.InputEvent, openSticky func(string, string, uint32) error, views map[string]view, pins *[]pinnedSurface, loadPins func() error) {
	fail := func(err error) {
		if err != nil {
			sess.Notify(err.Error())
		}
	}
	failName := func(_ string, err error) { fail(err) }
	sess.ClearNotice()
	snap := sess.Snap()
	sel := snap.Selected
	switch {
	case m.Node == "open":
		_, err := hostcall.Call(ctx, c, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID})
		fail(err)
	case m.Node == "omnibox" && m.Event == v1.EventSubmit:
		failName(sess.SubmitQuery())
	case m.Node == "omnibox":
		sess.Search(m.Text)
	case m.Node == "new":
		failName(sess.CreateFromQuery())
	case m.Node == "clipboard-import":
		var result v1.ClipboardReadResult
		if err := call(ctx, c, v1.CallClipboardRead, v1.ClipboardReadParams{}, &result); err != nil {
			fail(err)
		} else if strings.TrimSpace(result.Text) == "" {
			sess.Notify("The clipboard has no plain text")
		} else {
			failName(sess.CreateWithBody(result.Text))
		}
	case m.Node == "launcher-capture":
		if strings.TrimSpace(m.Text) != "" {
			failName(sess.CreateWithBody(m.Text))
		}
	case m.Node == "scratch":
		fail(sess.OpenScratch())
	case m.Node == "notice-dismiss":
		sess.DismissNotice()
	case m.Node == "sort":
		sess.ToggleSort()
	case m.Node == "title" && m.Event == v1.EventSubmit:
		sess.SetTitleDraft(m.Text)
		fail(sess.CommitTitle())
	case m.Node == "title":
		sess.SetTitleDraft(m.Text)
	case m.Node == "body":
		fail(sess.Type(m.Text))
	case m.Node == "favorite" && sel != "":
		fail(sess.SetFavorite(sel, !snap.Favorite))
	case m.Node == "sticky" && sel != "":
		fail(openSticky(sel, m.Output, m.Generation))
	case m.Node == "delete" && sel != "":
		sess.ProposeDelete(sel)
	case m.Node == "cancel":
		sess.CancelPending()
	case m.Node == "confirm-delete":
		name, err := sess.ConfirmDeleteName()
		fail(err)
		if err == nil {
			fail(closeDeleted(ctx, c, name, views, pins, loadPins))
		}
	case m.Node == "reload":
		fail(sess.Reload())
	case m.Node == "keep":
		fail(sess.KeepLocal())
	case m.Node == "save":
		fail(sess.SaveNow())
	case m.Node == "restore":
		fail(sess.Restore())
	case m.Node == "discard":
		fail(sess.Discard())
	case strings.HasPrefix(m.Node, "open:"):
		if name := findNoteName(snap.Notes, strings.TrimPrefix(m.Node, "open:")); name != "" {
			fail(sess.Select(name))
		}
	}
}

func handleSticky(ctx context.Context, c *v1.Client, sess *notes.Session, m *v1.InputEvent, v view, views map[string]view, pins *[]pinnedSurface, loadPins func() error) {
	fail := func(err error) {
		if err != nil {
			sess.ReportNoteError(v.name, err.Error())
		}
	}
	switch {
	case strings.HasPrefix(m.Node, "sticky-body:"):
		fail(sess.TypeNote(v.name, m.Text))
	case strings.HasPrefix(m.Node, "color:"):
		parts := strings.Split(m.Node, ":")
		if len(parts) != 3 || parts[1] != notes.Token(v.name) {
			return
		}
		color := validColor(parts[2])
		if color != parts[2] {
			return
		}
		if err := setState(ctx, c, colorStateBase+notes.Token(v.name), color); err != nil {
			fail(err)
			return
		}
		v.color = color
		views[m.ViewID] = v
	case m.Node == "surface-pin":
		if err := loadPins(); err != nil {
			fail(err)
			return
		}
		on := !v.pinned
		updated := setPinned(*pins, v.name, v.output, on)
		if err := setState(ctx, c, pinnedStateKey, updated); err != nil {
			fail(err)
			return
		}
		if err := call(ctx, c, v1.CallSurfacePin, v1.SurfacePinParams{View: m.ViewID, Pinned: on}, nil); err != nil {
			if rollbackErr := setState(ctx, c, pinnedStateKey, *pins); rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("restore saved pin state: %w", rollbackErr))
			}
			fail(err)
			return
		}
		*pins = updated
		v.pinned = on
		views[m.ViewID] = v
	case m.Node == "surface-close":
		if err := sess.FlushNote(v.name); err != nil {
			fail(err)
			return
		}
		fail(call(ctx, c, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: m.ViewID}, nil))
	}
}

// retargetStickies points every sticky on oldName at newName. It runs before
// anything that can fail, so a sticky never edits a name the session no
// longer holds.
func retargetStickies(views map[string]view, oldName, newName string) {
	for id, v := range views {
		if v.kind == v1.ViewFloating && v.name == oldName {
			v.name = newName
			views[id] = v
		}
	}
}

func closeDeleted(ctx context.Context, c *v1.Client, name string, views map[string]view, pins *[]pinnedSurface, loadPins func() error) error {
	var closeErr error
	for id, v := range views {
		if v.kind == v1.ViewFloating && v.name == name {
			if err := call(ctx, c, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: id}, nil); err != nil {
				closeErr = errors.Join(closeErr, err)
				continue
			}
			// Gone from the map now, or the next refresh asks the session
			// for the deleted file and reports it as an error.
			delete(views, id)
		}
	}
	if err := setState(ctx, c, keyStateBase+notes.Token(name), nil); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := setState(ctx, c, colorStateBase+notes.Token(name), nil); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := loadPins(); err != nil {
		closeErr = errors.Join(closeErr, err)
	} else {
		updated := setPinned(*pins, name, "", false)
		*pins = updated
		if err := setState(ctx, c, pinnedStateKey, updated); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

// stickyKey is the shell surface key for name's sticky. The shell keeps a
// sticky's position and size under it, so a rename carries it to the new name
// (see migrateNoteState); a note never renamed uses the default.
func stickyKey(ctx context.Context, c *v1.Client, name string) (string, error) {
	key := "note:" + notes.Token(name)
	_, err := getState(ctx, c, keyStateBase+notes.Token(name), &key)
	return key, err
}

func migrateNoteState(ctx context.Context, c *v1.Client, oldName, newName string, pins *[]pinnedSurface, loadPins func() error) error {
	key, err := stickyKey(ctx, c, oldName)
	if err != nil {
		return err
	}
	if err := setState(ctx, c, keyStateBase+notes.Token(newName), key); err != nil {
		return err
	}
	// The old name may be reused by a new note, which must not inherit the
	// renamed sticky's geometry.
	fresh := fmt.Sprintf("note:%s:%d", notes.Token(oldName), time.Now().UnixNano())
	if err := setState(ctx, c, keyStateBase+notes.Token(oldName), fresh); err != nil {
		return err
	}
	color := "sun"
	found, err := getState(ctx, c, colorStateBase+notes.Token(oldName), &color)
	if err != nil {
		return err
	}
	if found {
		if err := setState(ctx, c, colorStateBase+notes.Token(newName), validColor(color)); err != nil {
			return err
		}
		if err := setState(ctx, c, colorStateBase+notes.Token(oldName), nil); err != nil {
			return err
		}
	}
	if err := loadPins(); err != nil {
		return err
	}
	updated := make([]pinnedSurface, 0, len(*pins))
	for _, p := range *pins {
		if p.Name == oldName {
			p.Name = newName
		}
		updated = append(updated, p)
	}
	*pins = normalizePins(updated)
	return setState(ctx, c, pinnedStateKey, *pins)
}

func findNoteName(items []notes.Summary, token string) string {
	for _, item := range items {
		if notes.Token(item.Name) == token {
			return item.Name
		}
	}
	return ""
}

func normalizePins(pins []pinnedSurface) []pinnedSurface {
	seen := map[pinnedSurface]bool{}
	out := make([]pinnedSurface, 0, len(pins))
	for _, p := range pins {
		if p.Name == "" || p.Output == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Output != out[j].Output {
			return out[i].Output < out[j].Output
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func isPinned(pins []pinnedSurface, name, output string) bool {
	for _, p := range pins {
		if p.Name == name && p.Output == output {
			return true
		}
	}
	return false
}

func setPinned(pins []pinnedSurface, name, output string, on bool) []pinnedSurface {
	out := make([]pinnedSurface, 0, len(pins)+1)
	for _, p := range pins {
		if p.Name != name || (output != "" && p.Output != output) {
			out = append(out, p)
		}
	}
	if on && output != "" {
		out = append(out, pinnedSurface{Name: name, Output: output})
	}
	return normalizePins(out)
}

func validColor(color string) string {
	switch color {
	case "sun", "mint", "sky", "rose", "lilac":
		return color
	default:
		return "sun"
	}
}

func getState(ctx context.Context, c *v1.Client, key string, dst any) (bool, error) {
	var result v1.StateGetResult
	if err := call(ctx, c, v1.CallStateGet, v1.StateGetParams{Key: key}, &result); err != nil {
		return false, err
	}
	if !result.Found || len(result.Value) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(result.Value, dst); err != nil {
		return false, fmt.Errorf("notes: read state %s: %w", key, err)
	}
	return true, nil
}

func setState(ctx context.Context, c *v1.Client, key string, value any) error {
	var raw json.RawMessage
	if value == nil {
		raw = json.RawMessage("null")
	} else {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		raw = encoded
	}
	return call(ctx, c, v1.CallStateSet, v1.StateSetParams{Key: key, Value: raw}, nil)
}

func call(ctx context.Context, c *v1.Client, kind v1.CallKind, params, result any) error {
	reply, err := hostcall.Call(ctx, c, kind, params)
	if err != nil {
		return err
	}
	if !reply.OK {
		return errors.New(reply.Error)
	}
	if result != nil && len(reply.Result) != 0 {
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("notes: invalid %s reply: %w", kind, err)
		}
	}
	return nil
}

func applySettings(sess *notes.Session, values map[string]any) *notes.Session {
	dir := "~/Documents/Notes"
	ext := "md"
	if values != nil {
		if v, ok := values["notes_dir"].(string); ok && v != "" {
			dir = v
		}
		if v, ok := values["extension"].(string); ok && v != "" {
			ext = v
		}
	}
	st, err := notes.Open(dir, ext)
	if err != nil {
		if sess == nil {
			sess = notes.NewSession(nil, time.Now)
		}
		sess.ReportFolderError("Could not open notes folder: " + err.Error())
		return sess
	}
	if sess == nil {
		return notes.NewSession(st, time.Now)
	}
	if err := sess.SetStore(st); err != nil {
		sess.ReportFolderError("Could not change notes folder: " + err.Error())
	}
	return sess
}
