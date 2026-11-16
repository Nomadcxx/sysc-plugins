package hermes

import (
	"context"
	"encoding/json"
	"fmt"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The pinned surface is the chat panel lifted into a floating window. The
// shell persists its geometry; the plugin only names the key and asks for
// the plan's 640x720 near the top-left.
const (
	pinnedKey      = "pinned"
	pinnedTitle    = "Hermes"
	pinnedWidth    = 640
	pinnedHeight   = 720
	pinnedX        = 32
	pinnedY        = 32
	pinnedStateKey = "pinned"
	pinnedEntry    = "panel"
)

// PinnedTree draws the chat panel's header, approval card, transcript and
// composer as a floating view. It reuses the panel's builders so both
// surfaces cannot drift; the action row and sections stay panel-only.
func PinnedTree(p PanelState, width, height int) *v1.Node {
	p.Pinned = true
	inner := width - 2*panelPad
	header := panelHeader(p, DeriveStatus(p.Summary.BarState), inner)
	composer := panelComposer(p, turnRunning(p), inner)
	var approval *v1.Node
	if p.Summary.ApprovalPending {
		approval = approvalCard(p, inner)
	}

	children := []*v1.Node{header}
	if approval != nil {
		children = append(children, approval)
	}
	used, count := 2*panelPad, 1 // the transcript
	for _, n := range []*v1.Node{header, approval, composer} {
		if n != nil {
			used += n.Height
			count++
		}
	}
	transcriptH := height - used - (count-1)*panelGap
	if transcriptH < minTranscriptH {
		transcriptH = minTranscriptH
	}
	children = append(children, transcript(p, inner, transcriptH), composer)
	return &v1.Node{
		Kind: v1.KindColumn, Width: width, Height: height,
		Padding: panelPad, Gap: panelGap, Children: children,
	}
}

// surfaceHost is the slice of the v1 client the pinned surface needs, so
// tests can answer host calls without a plugin process.
type surfaceHost interface {
	Call(ctx context.Context, kind v1.CallKind, params any) (v1.HostReply, error)
}

// PinnedSurface owns the floating surface's view and the saved pin state.
type PinnedSurface struct {
	host   surfaceHost
	viewID string
	pinned bool
}

// NewPinnedSurface starts unpinned; Restore reads the saved state.
func NewPinnedSurface(host surfaceHost) *PinnedSurface {
	return &PinnedSurface{host: host}
}

// Pinned reports whether the floating surface should be open.
func (s *PinnedSurface) Pinned() bool { return s.pinned }

// ViewID is the host's id for the open surface, empty when closed.
func (s *PinnedSurface) ViewID() string { return s.viewID }

// Pin opens (or raises) the floating surface and saves the state.
func (s *PinnedSurface) Pin(ctx context.Context) error {
	view, err := s.open(ctx)
	if err != nil {
		return err
	}
	s.viewID = view
	s.pinned = true
	if err := s.save(ctx, true); err != nil {
		s.viewID = ""
		s.pinned = false
		return err
	}
	return nil
}

// Unpin closes the floating surface and saves the state.
func (s *PinnedSurface) Unpin(ctx context.Context) error {
	view := s.viewID
	if view == "" {
		view = pinnedKey
	}
	reply, err := s.host.Call(ctx, v1.CallSurfaceClose, v1.SurfaceCloseParams{View: view})
	if err != nil {
		return err
	}
	if !reply.OK {
		return refused(v1.CallSurfaceClose, reply)
	}
	s.viewID = ""
	s.pinned = false
	return s.save(ctx, false)
}

// Restore reopens the surface when the saved state says it was pinned.
func (s *PinnedSurface) Restore(ctx context.Context) error {
	reply, err := s.host.Call(ctx, v1.CallStateGet, v1.StateGetParams{Key: pinnedStateKey})
	if err != nil {
		return err
	}
	if !reply.OK {
		return refused(v1.CallStateGet, reply)
	}
	var result v1.StateGetResult
	if len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, &result); err != nil {
			return fmt.Errorf("state.get: %w", err)
		}
	}
	if !result.Found || len(result.Value) == 0 {
		return nil
	}
	var pinned bool
	if err := json.Unmarshal(result.Value, &pinned); err != nil {
		return fmt.Errorf("state.get %s: %w", pinnedStateKey, err)
	}
	if !pinned {
		return nil
	}
	view, err := s.open(ctx)
	if err != nil {
		return err
	}
	s.viewID = view
	s.pinned = true
	return nil
}

// Open is what the bar, a service action and the launcher call: a pinned
// surface is raised instead of opening the chat panel.
func (s *PinnedSurface) Open(ctx context.Context) error {
	if !s.pinned {
		reply, err := s.host.Call(ctx, v1.CallPanelOpen, v1.PanelParams{Entry: pinnedEntry})
		if err != nil {
			return err
		}
		if !reply.OK {
			return refused(v1.CallPanelOpen, reply)
		}
		return nil
	}
	if _, err := s.open(ctx); err != nil {
		return err
	}
	return nil
}

// open asks the host to open or focus the surface and returns its view.
func (s *PinnedSurface) open(ctx context.Context) (string, error) {
	reply, err := s.host.Call(ctx, v1.CallSurfaceOpen, v1.SurfaceOpenParams{
		Key: pinnedKey, Title: pinnedTitle,
		X: pinnedX, Y: pinnedY, Width: pinnedWidth, Height: pinnedHeight,
	})
	if err != nil {
		return "", err
	}
	if !reply.OK {
		return "", refused(v1.CallSurfaceOpen, reply)
	}
	var result v1.SurfaceResult
	if len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, &result); err != nil {
			return "", fmt.Errorf("surface.open: %w", err)
		}
	}
	if result.ViewID == "" {
		return "", fmt.Errorf("surface.open: host returned no view")
	}
	return result.ViewID, nil
}

// save persists whether the surface is pinned in plugin state.
func (s *PinnedSurface) save(ctx context.Context, pinned bool) error {
	value, err := json.Marshal(pinned)
	if err != nil {
		return err
	}
	reply, err := s.host.Call(ctx, v1.CallStateSet, v1.StateSetParams{Key: pinnedStateKey, Value: value})
	if err != nil {
		return err
	}
	if !reply.OK {
		return refused(v1.CallStateSet, reply)
	}
	return nil
}

func refused(kind v1.CallKind, reply v1.HostReply) error {
	if reply.Error != "" {
		return fmt.Errorf("%s: %s", kind, reply.Error)
	}
	return fmt.Errorf("%s: refused", kind)
}
