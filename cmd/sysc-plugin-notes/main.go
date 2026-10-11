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

type notesActionResult struct {
	kind      string
	clipboard v1.ClipboardReadResult
	err       error
	token     string
	viewID    string
	sticky    view
	opened    []notesOpenedSticky
	removed   []string
	pins      []pinnedSurface
	pinsReady bool
	target    notesRestoreTarget
	color     string
	pinned    bool
}

type notesOpenedSticky struct {
	viewID string
	view   view
}

type notesRestoreTarget struct {
	output     string
	generation uint32
}

type notesIOJob func(context.Context) notesActionResult
type notesSubmit func(notesIOJob) error

type notesDeleteCommit struct {
	name string
	err  error
}

// ponytail: cap shutdown at one host-call deadline; stronger guarantees for queued host writes need durable storage, not unbounded process exit.
const notesActionDrainTimeout = 5 * time.Second

func startNotesActionWorker(ctx context.Context, jobs <-chan notesIOJob, results chan<- notesActionResult) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for job := range jobs {
			if ctx.Err() != nil {
				return
			}
			result := job(ctx)
			select {
			case results <- result:
			case <-ctx.Done():
				return
			}
		}
	}()
	return done
}

func drainNotesActionWorker(jobs chan notesIOJob, results <-chan notesActionResult, done <-chan struct{}, cancel context.CancelFunc) {
	close(jobs)
	defer cancel()
	timer := time.NewTimer(notesActionDrainTimeout)
	defer timer.Stop()
	for {
		select {
		case <-done:
			return
		case <-results:
		case <-timer.C:
			return
		}
	}
}

func submitNotesDelete(submit notesSubmit, commit func() (string, error), action func(context.Context, string) notesActionResult) error {
	committed := make(chan notesDeleteCommit, 1)
	if err := submit(func(ctx context.Context) notesActionResult {
		result := <-committed
		if result.err != nil {
			return notesActionResult{kind: "delete-note"}
		}
		return action(ctx, result.name)
	}); err != nil {
		return err
	}
	result := notesDeleteCommit{}
	result.name, result.err = commit()
	committed <- result
	return result.err
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

func readPins(ctx context.Context, c *v1.Client) ([]pinnedSurface, error) {
	var pins []pinnedSurface
	if _, err := getState(ctx, c, pinnedStateKey, &pins); err != nil {
		return nil, err
	}
	return normalizePins(pins), nil
}

func openStickyHost(ctx context.Context, c *v1.Client, name, title, output string, generation uint32, x, y, slot int, pins []pinnedSurface) (string, view, error) {
	color := "sun"
	if _, err := getState(ctx, c, colorStateBase+notes.Token(name), &color); err != nil {
		return "", view{}, err
	}
	key, err := stickyKey(ctx, c, name)
	if err != nil {
		return "", view{}, err
	}
	params := v1.SurfaceOpenParams{Key: key, Title: title, Output: output, Generation: generation, X: x, Y: y, Width: stickyWidth, Height: stickyHeight}
	var result v1.SurfaceResult
	if err := call(ctx, c, v1.CallSurfaceOpen, params, &result); err != nil {
		return "", view{}, err
	}
	if result.ViewID == "" {
		return "", view{}, errors.New("notes: shell did not return a sticky view")
	}
	color = validColor(color)
	pinned := isPinned(pins, name, output)
	v := view{kind: v1.ViewFloating, name: name, color: color, pinned: pinned, output: output, generation: generation, slot: slot, hasSlot: true}
	return result.ViewID, v, call(ctx, c, v1.CallSurfacePin, v1.SurfacePinParams{View: result.ViewID, Pinned: pinned}, nil)
}

func resolveSticky(ctx context.Context, c *v1.Client, entry, output string, generation uint32, known []view) (view, error) {
	pins, err := readPins(ctx, c)
	if err != nil {
		return view{}, err
	}
	candidates := make([]view, 0, len(pins)+len(known))
	for _, pin := range pins {
		candidates = append(candidates, view{kind: v1.ViewFloating, name: pin.Name, output: pin.Output, pinned: true})
	}
	candidates = append(candidates, known...)
	for _, candidate := range candidates {
		if candidate.kind != v1.ViewFloating || candidate.name == "" {
			continue
		}
		key, err := stickyKey(ctx, c, candidate.name)
		if err != nil {
			return view{}, err
		}
		if key != entry {
			continue
		}
		color := candidate.color
		if color == "" {
			color = "sun"
			if _, err := getState(ctx, c, colorStateBase+notes.Token(candidate.name), &color); err != nil {
				return view{}, err
			}
		}
		candidate.color = validColor(color)
		candidate.output, candidate.generation = output, generation
		candidate.pinned = isPinned(pins, candidate.name, output)
		return candidate, nil
	}
	return view{}, errors.New("notes: could not identify a restored sticky note")
}

func pinSticky(ctx context.Context, c *v1.Client, id string, current view) ([]pinnedSurface, bool, error) {
	pins, err := readPins(ctx, c)
	if err != nil {
		return nil, false, err
	}
	on := !isPinned(pins, current.name, current.output)
	updated := setPinned(pins, current.name, current.output, on)
	if err := setState(ctx, c, pinnedStateKey, updated); err != nil {
		return pins, false, err
	}
	if err := call(ctx, c, v1.CallSurfacePin, v1.SurfacePinParams{View: id, Pinned: on}, nil); err != nil {
		if rollbackErr := setState(ctx, c, pinnedStateKey, pins); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("restore saved pin state: %w", rollbackErr))
		}
		return pins, false, err
	}
	return updated, on, nil
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
	closedViews := map[string]bool{}
	pub := publisher{}
	var pins []pinnedSurface
	pinsLoaded := false
	ctx, cancel := context.WithCancel(context.Background())
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	incoming := make(chan v1.Message, 8)
	// ponytail: cap queued host and filesystem jobs at 32; overflow is visible and retried on the next output view open.
	actionJobs := make(chan notesIOJob, 32)
	actionResults := make(chan notesActionResult, 32)
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
	actionWorkerDone := startNotesActionWorker(workerCtx, actionJobs, actionResults)
	defer func() {
		cancel()
		drainNotesActionWorker(actionJobs, actionResults, actionWorkerDone, cancelWorker)
		if sess != nil {
			_ = sess.Close()
		}
	}()
	ticks := time.NewTicker(time.Second)
	defer ticks.Stop()

	submit := func(job notesIOJob) error {
		select {
		case actionJobs <- job:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			return errors.New("Notes is busy; try again")
		}
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
				if v.name == "" {
					continue
				}
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

	pendingOpen := map[string]view{}
	var openSequence uint64
	openSticky := func(name, output string, generation uint32) error {
		if output == "" || generation == 0 {
			return errors.New("notes: output identity is unavailable for a sticky note")
		}
		s := ensure()
		doc, err := s.Document(name)
		if err != nil {
			return err
		}
		s.Keep(name)
		occupied := make(map[string]view, len(views)+len(pendingOpen))
		for id, v := range views {
			occupied[id] = v
		}
		for token, v := range pendingOpen {
			occupied["pending:"+token] = v
		}
		x, y, slot := cascade(occupied, output)
		openSequence++
		token := fmt.Sprintf("%d", openSequence)
		pendingOpen[token] = view{kind: v1.ViewFloating, name: name, output: output, slot: slot, hasSlot: true}
		if err := submit(func(workCtx context.Context) notesActionResult {
			pins, err := readPins(workCtx, c)
			if err != nil {
				return notesActionResult{kind: "sticky-open", token: token, err: err}
			}
			id, sticky, err := openStickyHost(workCtx, c, name, notes.DisplayTitle(name, doc.Body), output, generation, x, y, slot, pins)
			return notesActionResult{kind: "sticky-open", token: token, viewID: id, sticky: sticky, pins: pins, pinsReady: true, err: err}
		}); err != nil {
			delete(pendingOpen, token)
			return err
		}
		return nil
	}

	restorePending := map[string]bool{}
	pendingPins := map[string]bool{}
	targetKey := func(target notesRestoreTarget) string {
		return fmt.Sprintf("%s:%d", target.output, target.generation)
	}
	queueRestoredPins := func(target notesRestoreTarget, saved []pinnedSurface) {
		if target.output == "" || target.generation == 0 {
			return
		}
		for _, pin := range saved {
			if pin.Output != target.output {
				continue
			}
			alreadyOpen := false
			for _, v := range views {
				if v.kind == v1.ViewFloating && v.name == pin.Name && v.output == pin.Output {
					alreadyOpen = true
					break
				}
			}
			for _, v := range pendingOpen {
				if v.name == pin.Name && v.output == pin.Output {
					alreadyOpen = true
					break
				}
			}
			if !alreadyOpen {
				if err := openSticky(pin.Name, target.output, target.generation); err != nil {
					ensure().Notify("Could not restore " + pin.Name + ": " + err.Error())
				}
			}
		}
	}
	restorePins := func(output string, generation uint32) {
		target := notesRestoreTarget{output: output, generation: generation}
		if output == "" || generation == 0 {
			return
		}
		if pinsLoaded {
			queueRestoredPins(target, pins)
			return
		}
		key := targetKey(target)
		if restorePending[key] {
			return
		}
		restorePending[key] = true
		if err := submit(func(workCtx context.Context) notesActionResult {
			saved, err := readPins(workCtx, c)
			return notesActionResult{kind: "restore-pins", target: target, pins: saved, pinsReady: err == nil, err: err}
		}); err != nil {
			delete(restorePending, key)
			ensure().Notify("Could not restore sticky notes: " + err.Error())
		}
	}

	// Rename state and surface calls share the ordered host worker. The owner
	// retargets note edits immediately, then applies only completed host results.
	afterRename := func(oldName, newName string) {
		retargetStickies(views, oldName, newName)
		type renamedSticky struct {
			id string
			v  view
		}
		var reopen []renamedSticky
		for id, v := range views {
			if v.kind == v1.ViewFloating && v.name == newName {
				reopen = append(reopen, renamedSticky{id: id, v: v})
			}
		}
		doc, docErr := sess.Document(newName)
		if err := submit(func(workCtx context.Context) notesActionResult {
			var workerPins []pinnedSurface
			pinsReady := false
			loadWorkerPins := func() error {
				if pinsReady {
					return nil
				}
				var err error
				workerPins, err = readPins(workCtx, c)
				pinsReady = err == nil
				return err
			}
			if err := migrateNoteState(workCtx, c, oldName, newName, &workerPins, loadWorkerPins); err != nil {
				return notesActionResult{kind: "rename", pins: workerPins, pinsReady: pinsReady, err: err}
			}
			result := notesActionResult{kind: "rename", pins: workerPins, pinsReady: pinsReady}
			if docErr != nil {
				result.err = docErr
				return result
			}
			for _, item := range reopen {
				if err := call(workCtx, c, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: item.id}, nil); err != nil {
					result.err = errors.Join(result.err, err)
					continue
				}
				result.removed = append(result.removed, item.id)
				x, y := stickyOriginX, stickyOriginY
				slot := item.v.slot
				if item.v.hasSlot {
					x += slot * stickyStep
					y += slot * stickyStep
				}
				id, opened, err := openStickyHost(workCtx, c, newName, notes.DisplayTitle(newName, doc.Body), item.v.output, item.v.generation, x, y, slot, workerPins)
				if id != "" {
					result.opened = append(result.opened, notesOpenedSticky{viewID: id, view: opened})
				}
				result.err = errors.Join(result.err, err)
			}
			return result
		}); err != nil {
			sess.Notify("Could not move sticky settings to the new name: " + err.Error())
		}
	}
	closeNote := func() error {
		workerViews := make(map[string]view, len(views))
		viewIDs := make([]string, 0, len(views))
		for id, v := range views {
			workerViews[id] = v
			viewIDs = append(viewIDs, id)
		}
		workerPins := append([]pinnedSurface(nil), pins...)
		pinsReady := pinsLoaded
		return submitNotesDelete(submit, sess.ConfirmDeleteName, func(workCtx context.Context, name string) notesActionResult {
			loadWorkerPins := func() error {
				if pinsReady {
					return nil
				}
				var err error
				workerPins, err = readPins(workCtx, c)
				pinsReady = err == nil
				return err
			}
			err := closeDeleted(workCtx, c, name, workerViews, &workerPins, loadWorkerPins)
			result := notesActionResult{kind: "delete-note", pins: workerPins, pinsReady: pinsReady, err: err}
			for _, id := range viewIDs {
				if _, stillOpen := workerViews[id]; !stillOpen {
					result.removed = append(result.removed, id)
				}
			}
			return result
		})
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks.C:
			if sess != nil && editableOpen() {
				sess.Tick()
				snapshot()
			}
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				ensure()
				delete(closedViews, m.ViewID)
				v := view{kind: m.View, output: m.Output, generation: m.Generation}
				if old, ok := views[m.ViewID]; ok {
					v = old
					v.kind, v.output, v.generation = m.View, m.Output, m.Generation
				}
				if m.View == v1.ViewFloating {
					if v.name != "" {
						views[m.ViewID] = v
						pub.forget(m.ViewID)
						snapshot()
						continue
					}
					known := make([]view, 0, len(views))
					for _, candidate := range views {
						if candidate.kind == v1.ViewFloating && candidate.name != "" {
							known = append(known, candidate)
						}
					}
					for _, candidate := range pendingOpen {
						known = append(known, candidate)
					}
					if err := submit(func(workCtx context.Context) notesActionResult {
						resolved, err := resolveSticky(workCtx, c, m.Entry, m.Output, m.Generation, known)
						return notesActionResult{kind: "resolve-sticky", viewID: m.ViewID, sticky: resolved, err: err}
					}); err != nil {
						sess.Notify("Could not load sticky note state: " + err.Error())
					}
					views[m.ViewID] = v
					pub.forget(m.ViewID)
					snapshot()
					continue
				}
				views[m.ViewID] = v
				pub.forget(m.ViewID)
				snapshot()
				if m.View != v1.ViewFloating {
					restorePins(m.Output, m.Generation)
					snapshot()
				}
			case *v1.ViewClose:
				closedViews[m.ViewID] = true
				delete(pendingPins, m.ViewID)
				if v, ok := views[m.ViewID]; ok {
					if v.kind == v1.ViewFloating && v.name != "" {
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
					handleSticky(ctx, c, sess, m, v, views, pendingPins, submit)
				} else {
					handlePanel(ctx, c, sess, m, openSticky, closeNote, actionResults)
				}
				if from, to, ok := sess.TakeRename(); ok {
					afterRename(from, to)
				}
				snapshot()
			case *v1.SettingsChanged:
				sess = applySettings(sess, m.Values)
				snapshot()
			}
		case result := <-actionResults:
			switch result.kind {
			case "open":
				if result.err != nil {
					ensure().Notify(result.err.Error())
				}
			case "clipboard-import":
				if result.err != nil {
					ensure().Notify(result.err.Error())
				} else if strings.TrimSpace(result.clipboard.Text) == "" {
					ensure().Notify("The clipboard has no plain text")
				} else if _, err := ensure().CreateWithBody(result.clipboard.Text); err != nil {
					ensure().Notify(err.Error())
				}
			case "restore-pins":
				delete(restorePending, targetKey(result.target))
				if result.err != nil {
					ensure().Notify("Could not restore sticky notes: " + result.err.Error())
				} else {
					pins, pinsLoaded = result.pins, true
					queueRestoredPins(result.target, pins)
				}
			case "sticky-open":
				delete(pendingOpen, result.token)
				if result.pinsReady {
					pins, pinsLoaded = result.pins, true
				}
				if result.viewID != "" && !closedViews[result.viewID] {
					views[result.viewID] = result.sticky
					pub.forget(result.viewID)
				}
				if result.err != nil {
					ensure().Notify(result.err.Error())
				}
			case "resolve-sticky":
				if result.err != nil {
					delete(views, result.viewID)
					pub.forget(result.viewID)
					ensure().Notify(result.err.Error())
				} else if _, exists := views[result.viewID]; exists {
					views[result.viewID] = result.sticky
				}
			case "sticky-color":
				if result.err != nil {
					if v, ok := views[result.viewID]; ok {
						sess.ReportNoteError(v.name, result.err.Error())
					}
				} else if v, ok := views[result.viewID]; ok && v.name == result.sticky.name {
					v.color = result.color
					views[result.viewID] = v
				}
			case "sticky-pin":
				delete(pendingPins, result.viewID)
				if result.err != nil {
					if v, ok := views[result.viewID]; ok {
						sess.ReportNoteError(v.name, result.err.Error())
					}
				} else {
					pins, pinsLoaded = result.pins, true
					if v, ok := views[result.viewID]; ok && v.name == result.sticky.name {
						v.pinned = result.pinned
						views[result.viewID] = v
					}
				}
			case "sticky-close":
				if result.err != nil {
					if v, ok := views[result.viewID]; ok {
						sess.ReportNoteError(v.name, result.err.Error())
					}
				}
			case "delete-note", "rename":
				for _, id := range result.removed {
					delete(views, id)
					pub.forget(id)
				}
				for _, opened := range result.opened {
					if !closedViews[opened.viewID] {
						views[opened.viewID] = opened.view
						pub.forget(opened.viewID)
					}
				}
				if result.pinsReady {
					pins, pinsLoaded = result.pins, true
				}
				if result.err != nil {
					if result.kind == "rename" {
						sess.Notify("Could not move sticky settings to the new name: " + result.err.Error())
					} else {
						sess.Notify(result.err.Error())
					}
				}
			}
			snapshot()
		}
	}
}

func handlePanel(ctx context.Context, c *v1.Client, sess *notes.Session, m *v1.InputEvent, openSticky func(string, string, uint32) error, closeNote func() error, results chan<- notesActionResult) {
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
		params := v1.PanelParams{Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID}
		go func() {
			_, err := hostcall.Call(ctx, c, v1.CallPanelOpen, params)
			select {
			case results <- notesActionResult{kind: "open", err: err}:
			case <-ctx.Done():
			}
		}()
	case m.Node == "omnibox" && m.Event == v1.EventSubmit:
		failName(sess.SubmitQuery())
	case m.Node == "omnibox":
		sess.Search(m.Text)
	case m.Node == "new":
		failName(sess.CreateFromQuery())
	case m.Node == "clipboard-import":
		go func() {
			var clipboard v1.ClipboardReadResult
			err := call(ctx, c, v1.CallClipboardRead, v1.ClipboardReadParams{}, &clipboard)
			select {
			case results <- notesActionResult{kind: "clipboard-import", clipboard: clipboard, err: err}:
			case <-ctx.Done():
			}
		}()
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
		fail(closeNote())
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

func handleSticky(ctx context.Context, c *v1.Client, sess *notes.Session, m *v1.InputEvent, v view, views map[string]view, pendingPins map[string]bool, submit notesSubmit) {
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
		if err := submit(func(workCtx context.Context) notesActionResult {
			err := setState(workCtx, c, colorStateBase+notes.Token(v.name), color)
			return notesActionResult{kind: "sticky-color", viewID: m.ViewID, sticky: v, color: color, err: err}
		}); err != nil {
			fail(err)
		}
	case m.Node == "surface-pin":
		if pendingPins[m.ViewID] {
			return
		}
		pendingPins[m.ViewID] = true
		if err := submit(func(workCtx context.Context) notesActionResult {
			updated, on, err := pinSticky(workCtx, c, m.ViewID, v)
			return notesActionResult{kind: "sticky-pin", viewID: m.ViewID, sticky: v, pins: updated, pinsReady: err == nil, pinned: on, err: err}
		}); err != nil {
			delete(pendingPins, m.ViewID)
			fail(err)
		}
	case m.Node == "surface-close":
		if err := sess.FlushNote(v.name); err != nil {
			fail(err)
			return
		}
		if err := submit(func(workCtx context.Context) notesActionResult {
			return notesActionResult{kind: "sticky-close", viewID: m.ViewID, err: call(workCtx, c, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: m.ViewID}, nil)}
		}); err != nil {
			fail(err)
		}
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
