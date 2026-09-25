package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/notes"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	pinnedStateKey = "sticky-pinned"
	colorStateBase = "sticky-color."
)

type view struct {
	kind       v1.ViewKind
	rev        uint64
	name       string
	color      string
	pinned     bool
	output     string
	generation uint32
}

type pinnedSurface struct {
	Name   string `json:"name"`
	Output string `json:"output"`
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.notes", Name: "Notes", Version: "1.0.0"})); err != nil {
		return err
	}
	var sess *notes.Session
	ensure := func() *notes.Session {
		if sess == nil {
			sess = applySettings(nil, nil)
		}
		return sess
	}
	views := map[string]view{}
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
		managerOpen := false
		for _, v := range views {
			if v.kind == v1.ViewPanel {
				managerOpen = true
				break
			}
		}
		var snap notes.Snapshot
		if managerOpen {
			snap = sess.Snap()
		}
		for id, v := range views {
			v.rev++
			views[id] = v
			var tree *v1.Node
			switch v.kind {
			case v1.ViewBar:
				tree = notes.BarTree()
			case v1.ViewTooltip:
				tree = notes.TooltipTree()
			case v1.ViewFloating:
				doc, err := sess.Document(v.name)
				if err != nil {
					sess.ReportError("Could not open sticky note: " + err.Error())
					continue
				}
				tree = notes.StickyTree(doc, v.color, v.pinned)
			default:
				tree = notes.PanelTree(snap)
			}
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
		if s == nil {
			return errors.New("notes: configured folder is unavailable")
		}
		if _, err := s.Document(name); err != nil {
			return err
		}
		if err := loadPins(); err != nil {
			return err
		}
		color := "sun"
		if _, err := getState(ctx, c, colorStateBase+notes.Token(name), &color); err != nil {
			return err
		}
		color = validColor(color)
		params := v1.SurfaceOpenParams{
			Key: "note:" + notes.Token(name), Title: strings.TrimSuffix(name, filepath.Ext(name)),
			Output: output, Generation: generation, X: 56, Y: 92, Width: 360, Height: 440,
		}
		var result v1.SurfaceResult
		if err := call(ctx, c, v1.CallSurfaceOpen, params, &result); err != nil {
			return err
		}
		if result.ViewID == "" {
			return errors.New("notes: shell did not return a sticky view")
		}
		pinned := isPinned(pins, name, output)
		views[result.ViewID] = view{kind: v1.ViewFloating, name: name, color: color, pinned: pinned, output: output, generation: generation}
		if err := call(ctx, c, v1.CallSurfacePin, v1.SurfacePinParams{View: result.ViewID, Pinned: pinned}, nil); err != nil {
			return err
		}
		return nil
	}

	restorePins := func(output string, generation uint32) {
		if output == "" || generation == 0 {
			return
		}
		if err := loadPins(); err != nil {
			if s := ensure(); s != nil {
				s.ReportError("Could not restore sticky notes: " + err.Error())
			}
			return
		}
		for _, pin := range pins {
			if pin.Output != output {
				continue
			}
			if err := openSticky(pin.Name, output, generation); err != nil {
				if s := ensure(); s != nil {
					s.ReportError("Could not restore " + pin.Name + ": " + err.Error())
				}
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
				if m.View == v1.ViewFloating {
					if err := loadPins(); err != nil {
						sess.ReportError("Could not load sticky note state: " + err.Error())
					} else {
						for _, pin := range pins {
							if "note:"+notes.Token(pin.Name) == m.Entry {
								v.name, v.pinned = pin.Name, isPinned(pins, pin.Name, m.Output)
								break
							}
						}
					}
					if v.name == "" {
						for _, candidate := range views {
							if candidate.kind == v1.ViewFloating && "note:"+notes.Token(candidate.name) == m.Entry {
								v.name, v.color, v.pinned = candidate.name, candidate.color, candidate.pinned
								break
							}
						}
					}
					if v.name == "" {
						sess.ReportError("Could not identify a restored sticky note")
						continue
					}
					if v.color == "" {
						v.color = "sun"
						_, _ = getState(ctx, c, colorStateBase+notes.Token(v.name), &v.color)
						v.color = validColor(v.color)
					}
				}
				views[m.ViewID] = v
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
							sess.ReportError("Save failed while closing Notes: " + err.Error())
						}
					}
				}
				delete(views, m.ViewID)
				snapshot()
			case *v1.ViewResync:
				if v, ok := views[m.ViewID]; ok {
					v.rev = 0
					views[m.ViewID] = v
				}
				snapshot()
			case *v1.InputEvent:
				if ensure() == nil {
					continue
				}
				if v, ok := views[m.ViewID]; ok && v.kind == v1.ViewFloating {
					handleSticky(ctx, c, sess, m, v, views, &pins, loadPins)
				} else {
					handlePanel(ctx, c, sess, m, openSticky, views, &pins, loadPins)
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
			sess.ReportError(err.Error())
		}
	}
	snap := sess.Snap()
	switch {
	case m.Node == "open":
		_, err := c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID})
		fail(err)
	case m.Node == "new":
		fail(sess.Create())
	case m.Node == "capture":
		sess.SetCaptureText(m.Text)
	case m.Node == "capture-save":
		if strings.TrimSpace(snap.CaptureText) != "" {
			fail(sess.Capture(snap.CaptureText))
		}
	case m.Node == "scratch":
		fail(sess.OpenScratch())
	case m.Node == "back":
		fail(sess.Back())
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
	case m.Node == "body":
		fail(sess.Type(m.Text))
	case m.Node == "title" && m.Event == v1.EventSubmit:
		old := sess.Current()
		fail(renameTitle(sess, m.Text))
		newName := sess.Current()
		if old != newName {
			stateErr := migrateNoteState(ctx, c, old, newName, pins, loadPins)
			fail(stateErr)
			for id, v := range views {
				if v.kind == v1.ViewFloating && v.name == old {
					v.name = newName
					views[id] = v
					if stateErr != nil {
						continue
					}
					if err := call(ctx, c, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: id}, nil); err != nil {
						fail(err)
						continue
					}
					delete(views, id)
					fail(openSticky(newName, v.output, v.generation))
				}
			}
		}
	case m.Node == "search":
		sess.Search(m.Text)
	case m.Node == "sort":
		sess.ToggleSort()
	case m.Node == "favorite-current":
		fail(sess.SetFavorite(snap.Current, !snap.Pinned))
	case m.Node == "sticky-current":
		fail(openSticky(snap.Current, m.Output, m.Generation))
	case m.Node == "delete-current":
		sess.ProposeDelete(snap.Current)
	case strings.HasPrefix(m.Node, "open:"):
		if name := findNoteName(snap.Notes, strings.TrimPrefix(m.Node, "open:")); name != "" {
			fail(sess.Open(name))
		}
	case strings.HasPrefix(m.Node, "sticky:"):
		if name := findNoteName(snap.Notes, strings.TrimPrefix(m.Node, "sticky:")); name != "" {
			fail(openSticky(name, m.Output, m.Generation))
		}
	case strings.HasPrefix(m.Node, "fav:"):
		if name := findNoteName(snap.Notes, strings.TrimPrefix(m.Node, "fav:")); name != "" {
			fav := false
			for _, item := range snap.Notes {
				if item.Name == name {
					fav = !item.Favorite
					break
				}
			}
			fail(sess.SetFavorite(name, fav))
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

func closeDeleted(ctx context.Context, c *v1.Client, name string, views map[string]view, pins *[]pinnedSurface, loadPins func() error) error {
	var closeErr error
	for id, v := range views {
		if v.kind == v1.ViewFloating && v.name == name {
			if err := call(ctx, c, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: id}, nil); err != nil {
				closeErr = errors.Join(closeErr, err)
			}
		}
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

func migrateNoteState(ctx context.Context, c *v1.Client, oldName, newName string, pins *[]pinnedSurface, loadPins func() error) error {
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
	reply, err := c.Call(ctx, kind, params)
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
		sess.ReportError("Could not open notes folder: " + err.Error())
		return sess
	}
	if sess == nil {
		return notes.NewSession(st, time.Now)
	}
	if err := sess.SetStore(st); err != nil {
		sess.ReportError("Could not change notes folder: " + err.Error())
	}
	return sess
}

func renameTitle(sess *notes.Session, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("notes: title cannot be empty")
	}
	cur := sess.Current()
	suffix := ".md"
	if dot := strings.LastIndex(cur, "."); dot >= 0 {
		suffix = cur[dot:]
	}
	if !strings.HasSuffix(strings.ToLower(title), strings.ToLower(suffix)) {
		title += suffix
	}
	return sess.Rename(title)
}
