package hermes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The settings panel is declared with include_settings, so the host wraps
// this tree in a scroll padded by PanelPadding and a column padded by
// CardPadding (pluginHost.panelTree), then appends the generated settings
// cards. The pads are the pinned sysc-shell v0.2.5 theme metrics, the same
// in every density (internal/theme/profile.go); a test re-reads them.
const (
	hostPanelPadding = 13
	hostCardPadding  = 9

	settingsGap     = 12
	settingsTitleH  = 28
	settingsCaption = lineHeight
	settingsLineGap = 2
	warningPad      = 8
)

// Noctalia's Test connection results (legacy-v4 i18n settings.test*).
const (
	ResultConnected   = "Connected"
	ResultUnreachable = "Unreachable: wrong host/port or bridge not running"
	ResultWrongToken  = "Wrong token"
	resultTesting     = "Testing..."
)

// FailedResult is Noctalia's "Failed:" result with the status code.
func FailedResult(code int) string { return fmt.Sprintf("Failed: %d", code) }

// SettingsInnerWidth is the width the host leaves this tree inside its
// scroll and card.
func SettingsInnerWidth(granted int) int {
	return granted - 2*hostPanelPadding - 2*hostCardPadding
}

// TokenSource names where the bridge token came from. The view shows the
// source, never the token.
type ConnectionTokenSource string

const (
	SourcePasted ConnectionTokenSource = "pasted"
	SourceFile   ConnectionTokenSource = "file"
	SourceLocal  ConnectionTokenSource = "local bridge.token"
	SourceNone   ConnectionTokenSource = "none"
)

// TokenCandidates are the tokens the runtime found: the pasted setting, the
// token file, and the local bridge's bridge.token.
type TokenCandidates struct {
	Pasted string
	File   string
	Local  string
}

// ConnectionInfo is the settings panel's connection line. It holds no
// token: errors are redacted when it is built.
type ConnectionInfo struct {
	ClientOnly  bool
	Host        string
	Port        int
	TokenSource ConnectionTokenSource
	LastError   string
	ViewError   string
}

const redacted = "[redacted]"

// ConnectionFor picks the token source the way the runtime uses tokens: in
// client-only mode the pasted token when it is not blank, else the token
// file; in local mode the local bridge.token. Every candidate token is
// redacted out of the last error and the last view.error.
func ConnectionFor(clientOnly bool, host string, port int, tokens TokenCandidates, lastErr, viewErr string) ConnectionInfo {
	source := SourceNone
	switch {
	case clientOnly && strings.TrimSpace(tokens.Pasted) != "":
		source = SourcePasted
	case clientOnly && strings.TrimSpace(tokens.File) != "":
		source = SourceFile
	case !clientOnly && strings.TrimSpace(tokens.Local) != "":
		source = SourceLocal
	}
	redact := func(s string) string {
		for _, tok := range []string{tokens.Pasted, tokens.File, tokens.Local} {
			if tok = strings.TrimSpace(tok); tok != "" {
				s = strings.ReplaceAll(s, tok, redacted)
			}
		}
		return strings.TrimSpace(s)
	}
	return ConnectionInfo{
		ClientOnly: clientOnly, Host: host, Port: port, TokenSource: source,
		LastError: redact(lastErr), ViewError: redact(viewErr),
	}
}

// BridgeProber is the runtime's GET with the current token: a status code,
// or an error when the bridge could not be reached.
type BridgeProber interface {
	Get(ctx context.Context, path string) (int, error)
}

// CheckConnection is Test connection: /health, then /state, which needs
// the token, so a wrong token is caught too (Noctalia only checked
// /health). It returns one of Noctalia's result strings.
func CheckConnection(ctx context.Context, p BridgeProber) string {
	for _, path := range []string{"/health", "/state"} {
		code, err := p.Get(ctx, path)
		switch {
		case err != nil:
			return ResultUnreachable
		case code == 403:
			return ResultWrongToken
		case code < 200 || code > 299:
			return FailedResult(code)
		}
	}
	return ResultConnected
}

var sshTargetPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.:-]+$`)

// ValidSSHTarget accepts user@host as the plan's pattern does, and also
// refuses a leading dash, which ssh would read as an option.
func ValidSSHTarget(s string) bool {
	return sshTargetPattern.MatchString(s) && !strings.HasPrefix(s, "-")
}

// SSHCommand is the tunnel the README describes, forwarding the bridge
// port to the bridge on the server's loopback.
func SSHCommand(target string, port int) (string, bool) {
	if !ValidSSHTarget(target) {
		return "", false
	}
	return fmt.Sprintf("ssh -N -L %d:127.0.0.1:%d %s", port, port, target), true
}

var errSSHTarget = errors.New("SSH target must look like user@host")

// CopySSHCommand writes the tunnel command to the clipboard through the
// host, bounded as every event-loop call is.
func CopySSHCommand(ctx context.Context, c *v1.Client, target string, port int) error {
	cmd, ok := SSHCommand(target, port)
	if !ok {
		return errSSHTarget
	}
	reply, err := hostcall.Call(ctx, c, v1.CallClipboardWrite, v1.ClipboardWriteParams{Text: cmd})
	if err != nil {
		return err
	}
	if !reply.OK {
		return fmt.Errorf("clipboard write refused: %s", reply.Error)
	}
	return nil
}

// CleartextWarning is the design's warning for client-only mode to a host
// off this machine. The plugin warns and does not block.
func CleartextWarning(clientOnly bool, host string) string {
	host = strings.TrimSpace(host)
	if !clientOnly || host == "" || IsLoopback(host) {
		return ""
	}
	return "Cleartext HTTP to " + host + ": the token and prompts are not encrypted."
}

// Detected holds what auto-configure recorded from /detect; client-only
// mode records only the provider and model.
type SettingsDetected struct {
	HermesHome    string
	HermesCommand string
	Provider      string
	Model         string
}

// SettingsState is everything the settings panel draws.
type SettingsState struct {
	Connection      ConnectionInfo
	Models          []ModelOption
	Picker          ModelPicker
	DefaultProvider string
	DefaultModel    string
	Pinned          bool
	TestResult      string
	Testing         bool
	Detected        SettingsDetected
	SSHTarget       string
}

// SettingsTree is the plugin's part of the settings panel, in the design's
// order: title, connection, model picker, Pin side panel, Test connection,
// Detected captions, the cleartext warning and Copy SSH command. It is width
// wide and as tall as its content; the host scroll owns any overflow.
func SettingsTree(s SettingsState, width int) *v1.Node {
	root := &v1.Node{Kind: v1.KindColumn, Width: width, Gap: settingsGap}
	add := func(n *v1.Node) {
		if n == nil {
			return
		}
		if len(root.Children) > 0 {
			root.Height += settingsGap
		}
		root.Height += n.Height
		root.Children = append(root.Children, n)
	}
	add(&v1.Node{Kind: v1.KindRow, Key: "title", Width: width, Height: settingsTitleH, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: menuIconSettings, IconSize: 20},
		{Kind: v1.KindText, Text: "Hermes Settings", Bold: true, Size: "title"},
	}})
	add(connectionBlock(s.Connection, width))
	add(settingsModelPicker(s, width))
	add(&v1.Node{Kind: v1.KindRow, Key: "pin-row", Width: width, Height: sectionButtonH, Gap: 8, Children: []*v1.Node{
		toggle("pin-side-panel", "Pin side panel", "Pin side panel", iconPin, s.Pinned, buttonWidth("Pin side panel", iconPin)),
	}})
	add(testConnectionBlock(s, width))
	add(detectedBlock(s.Detected, width))
	add(cleartextBlock(s.Connection, width))
	if s.Connection.ClientOnly {
		add(copySSHBlock(s, width))
	}
	return root
}

// captions stacks wrapped caption lines; tone colours every line.
func captions(key string, width int, tone v1.Tone, texts ...string) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Key: key, Width: width, Gap: settingsLineGap}
	for _, t := range texts {
		for _, line := range wrapLines(t, width/8, 4) {
			if len(col.Children) > 0 {
				col.Height += settingsLineGap
			}
			col.Height += settingsCaption
			col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: line, Size: "caption", Tone: tone, Height: settingsCaption})
		}
	}
	return col
}

func connectionBlock(c ConnectionInfo, width int) *v1.Node {
	mode := "Local"
	if c.ClientOnly {
		mode = "Client-only"
	}
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := c.Port
	if port == 0 {
		port = 19777
	}
	source := c.TokenSource
	if source == "" {
		source = SourceNone
	}
	col := captions("connection", width, v1.ToneNormal,
		fmt.Sprintf("%s · %s · token: %s", mode, net.JoinHostPort(strings.Trim(host, "[]"), fmt.Sprint(port)), source))
	for _, e := range []struct{ label, text string }{{"Last error: ", c.LastError}, {"Last view error: ", c.ViewError}} {
		if e.text == "" {
			continue
		}
		more := captions("", width, v1.ToneError, e.label+e.text)
		col.Height += settingsLineGap + more.Height
		col.Children = append(col.Children, more.Children...)
	}
	return col
}

func settingsModelPicker(s SettingsState, width int) *v1.Node {
	h := modelNaturalHeight(s.Models, s.Picker, s.DefaultProvider, s.DefaultModel)
	section := ModelSection(s.Models, s.Picker, s.DefaultProvider, s.DefaultModel, width, h)
	return &v1.Node{Kind: v1.KindColumn, Key: "model-picker", Width: width, Height: lineHeight + sectionGap + h, Gap: sectionGap,
		Children: []*v1.Node{{Kind: v1.KindText, Text: "Model", Bold: true, Size: "label", Height: lineHeight}, section}}
}

func testConnectionBlock(s SettingsState, width int) *v1.Node {
	b := autoButton("test-connection", "Test connection", "Test connection", modelIconPersist, sectionButtonH)
	b.Disabled = s.Testing
	col := &v1.Node{Kind: v1.KindColumn, Key: "test-connection-row", Width: width, Height: sectionButtonH, Gap: sectionGap,
		Children: []*v1.Node{{Kind: v1.KindRow, Width: width, Height: sectionButtonH, Children: []*v1.Node{b}}}}
	result, tone := s.TestResult, v1.ToneError
	switch {
	case s.Testing:
		result, tone = resultTesting, v1.ToneNormal
	case result == ResultConnected:
		tone = v1.ToneAccent
	}
	if result != "" {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText, Text: clip(result, width/8), Size: "caption", Tone: tone, Height: lineHeight})
		col.Height += sectionGap + lineHeight
	}
	return col
}

func detectedBlock(d SettingsDetected, width int) *v1.Node {
	var lines []string
	if d.HermesHome != "" {
		lines = append(lines, "Detected: Hermes home "+d.HermesHome)
	}
	if d.HermesCommand != "" {
		lines = append(lines, "Detected: Hermes command "+d.HermesCommand)
	}
	if d.Model != "" {
		model := d.Model
		if d.Provider != "" {
			model = d.Provider + " / " + d.Model
		}
		lines = append(lines, "Detected: default model "+model)
	}
	if len(lines) == 0 {
		return nil
	}
	return captions("detected", width, v1.ToneSubtle, lines...)
}

func cleartextBlock(c ConnectionInfo, width int) *v1.Node {
	text := CleartextWarning(c.ClientOnly, c.Host)
	if text == "" {
		return nil
	}
	textW := width - 2*warningPad - 16 - 8
	lines := captions("", textW, v1.ToneNormal, text)
	return &v1.Node{
		Kind: v1.KindRow, Key: "cleartext", Fill: "container", Radius: 8,
		Width: width, Height: lines.Height + 2*warningPad, Padding: warningPad, Gap: 8,
		Children: []*v1.Node{
			{Kind: v1.KindIcon, Icon: iconApproval, IconSize: 16, Tone: v1.ToneWarning, Name: "Cleartext warning"},
			lines,
		},
	}
}

func copySSHBlock(s SettingsState, width int) *v1.Node {
	cmd, ok := SSHCommand(s.SSHTarget, s.Connection.Port)
	b := autoButton("copy-ssh", "Copy SSH command", "Copy SSH command", iconOneShot, sectionButtonH)
	b.Disabled = !ok
	caption := "Set SSH target to user@host to copy the tunnel command"
	tone := v1.ToneSubtle
	if ok {
		caption, tone = cmd, v1.ToneNormal
	}
	lines := captions("", width, tone, caption)
	return &v1.Node{
		Kind: v1.KindColumn, Key: "copy-ssh-row", Width: width, Height: sectionButtonH + sectionGap + lines.Height, Gap: sectionGap,
		Children: append([]*v1.Node{{Kind: v1.KindRow, Width: width, Height: sectionButtonH, Children: []*v1.Node{b}}}, lines.Children...),
	}
}
