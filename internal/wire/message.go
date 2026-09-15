package v1

import "encoding/json"

// The version-one message names. Every line on the wire carries one of these
// in its "type" field, and a reader that does not recognise a name rejects the
// message rather than guessing.
const (
	// Host to plugin.
	TypeHostHello       = "host.hello"
	TypeHostShutdown    = "host.shutdown"
	TypeViewOpen        = "view.open"
	TypeViewClose       = "view.close"
	TypeViewResync      = "view.resync"
	TypeInputEvent      = "input.event"
	TypeSettingsChanged = "settings.changed"
	TypeHostReply       = "host.reply"

	// Plugin to host.
	TypePluginHello  = "plugin.hello"
	TypeViewSnapshot = "view.snapshot"
	TypeViewPatch    = "view.patch"
	TypeHostCall     = "host.call"
	TypePluginStatus = "plugin.status"
)

// Message is one framed protocol value. The interface method is unexported so
// that only this package can name what travels on the wire; a plugin still
// constructs and reads the concrete types freely.
type Message interface {
	messageType() string
}

// Version is one protocol version. The major number gates compatibility; a
// reader accepts unknown optional fields within a negotiated minor.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// Identity is the manifest identity both sides must agree on. The host sends
// what it launched and the plugin echoes what it believes it is; a mismatch
// means the directory and the executable have come apart.
type Identity struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Limits are the fixed ceilings the host enforces. They travel in the
// handshake so a plugin can stay inside them by construction instead of
// discovering them as disconnections.
type Limits struct {
	MaxMessageBytes    int `json:"max_message_bytes"`
	MaxNodes           int `json:"max_nodes"`
	MaxDepth           int `json:"max_depth"`
	MaxChildren        int `json:"max_children"`
	MaxTextBytes       int `json:"max_text_bytes"`
	MaxViews           int `json:"max_views"`
	MaxStateValueBytes int `json:"max_state_value_bytes"`
	MaxStateTotalBytes int `json:"max_state_total_bytes"`
	UpdatesPerSecond   int `json:"updates_per_second"`
	UpdateBurst        int `json:"update_burst"`
	PendingCalls       int `json:"pending_calls"`
}

// DefaultLimits is the version-one ceiling set from the milestone design.
var DefaultLimits = Limits{
	MaxMessageBytes:    MaxMessageBytes,
	MaxNodes:           MaxNodes,
	MaxDepth:           MaxDepth,
	MaxChildren:        MaxChildren,
	MaxTextBytes:       MaxTextBytes,
	MaxViews:           64,
	MaxStateValueBytes: 256 << 10,
	MaxStateTotalBytes: 4 << 20,
	UpdatesPerSecond:   60,
	UpdateBurst:        120,
	PendingCalls:       32,
}

// HostHello opens every connection. The plugin may send nothing before it
// answers.
type HostHello struct {
	Type string `json:"type"`
	// Supported lists the versions this host can speak, best first.
	Supported []Version `json:"supported"`
	Plugin    Identity  `json:"plugin"`
	// Capabilities are the grants the host has made, already narrowed to the
	// intersection of what the manifest requested and what the host offers.
	Capabilities []string `json:"capabilities"`
	Limits       Limits   `json:"limits"`
}

func (*HostHello) messageType() string { return TypeHostHello }

// PluginHello answers HostHello with the version the plugin selected from
// Supported and the capabilities it will actually use.
type PluginHello struct {
	Type         string   `json:"type"`
	Protocol     Version  `json:"protocol"`
	Plugin       Identity `json:"plugin"`
	Capabilities []string `json:"capabilities"`
}

func (*PluginHello) messageType() string { return TypePluginHello }

// HostShutdown asks for an orderly exit. The host closes standard input after
// it, waits, and then kills.
type HostShutdown struct {
	Type string `json:"type"`
}

func (*HostShutdown) messageType() string { return TypeHostShutdown }

// ViewOpen asks the plugin to supply one view. The host issues ViewID; the
// plugin never invents one.
type ViewOpen struct {
	Type   string   `json:"type"`
	ViewID string   `json:"view_id"`
	View   ViewKind `json:"view"`
	// Entry is the manifest widget or panel entry this view instantiates.
	Entry string `json:"entry"`
	// Instance is the stable placement identity, so two copies of one widget
	// can hold different settings while sharing a process.
	Instance string `json:"instance,omitempty"`
	// Output is the connector this view lives on. Views are per-output; the
	// plugin's state is not.
	Output string `json:"output,omitempty"`
	// Generation is the host's current identity for Output. A later host.call
	// that names an older generation fails rather than acting on a replug.
	Generation uint32 `json:"generation,omitempty"`
	// Width and Height are the space the host has reserved, in logical pixels.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

func (*ViewOpen) messageType() string { return TypeViewOpen }

// ViewClose retires a view. The plugin keeps running.
type ViewClose struct {
	Type   string `json:"type"`
	ViewID string `json:"view_id"`
}

func (*ViewClose) messageType() string { return TypeViewClose }

// ViewResync asks the plugin to send a snapshot after the host dropped or
// rejected a patch. The host emits it at most once until a snapshot arrives.
type ViewResync struct {
	Type   string `json:"type"`
	ViewID string `json:"view_id"`
}

func (*ViewResync) messageType() string { return TypeViewResync }

// Replacement is one keyed subtree in a ViewPatch.
type Replacement struct {
	Key  string `json:"key"`
	Node *Node  `json:"node"`
}

// ViewPatch replaces independent keyed subtrees at a matching base revision.
type ViewPatch struct {
	Type         string        `json:"type"`
	ViewID       string        `json:"view_id"`
	Base         uint64        `json:"base"`
	Revision     uint64        `json:"revision"`
	Replacements []Replacement `json:"replacements"`
}

func (*ViewPatch) messageType() string { return TypeViewPatch }

// ViewSnapshot replaces one view's whole tree at a new revision.
type ViewSnapshot struct {
	Type     string `json:"type"`
	ViewID   string `json:"view_id"`
	Revision uint64 `json:"revision"`
	Root     *Node  `json:"root"`
}

func (*ViewSnapshot) messageType() string { return TypeViewSnapshot }

// PointerButton names which button produced a pointer event.
type PointerButton string

const (
	ButtonPrimary   PointerButton = "primary"
	ButtonMiddle    PointerButton = "middle"
	ButtonSecondary PointerButton = "secondary"
)

// InputEvent reports one interaction against a node the plugin addressed by
// ID. It carries the revision the user actually saw, so a plugin can discard
// an event aimed at a tree it has already replaced.
type InputEvent struct {
	Type     string    `json:"type"`
	ViewID   string    `json:"view_id"`
	Revision uint64    `json:"revision"`
	Node     string    `json:"node"`
	Event    EventKind `json:"event"`
	// Button is set on pointer events only.
	Button PointerButton `json:"button,omitempty"`
	// Text is the committed value on change and submit events. The host owns
	// the live buffer; this is what it has committed.
	Text string `json:"text,omitempty"`
	// Output is the connector the event came from.
	Output string `json:"output,omitempty"`
	// Generation is Output's host identity at the time of the event.
	Generation uint32 `json:"generation,omitempty"`
}

func (*InputEvent) messageType() string { return TypeInputEvent }

// SettingsScope says whether values belong to the plugin as a whole or to one
// bar placement.
type SettingsScope string

const (
	ScopePlugin   SettingsScope = "plugin"
	ScopeInstance SettingsScope = "instance"
)

// SettingsChanged delivers committed settings. The host has already validated
// them against the manifest schema, so a plugin can use them without
// re-checking types it declared.
type SettingsChanged struct {
	Type     string         `json:"type"`
	Scope    SettingsScope  `json:"scope"`
	Instance string         `json:"instance,omitempty"`
	Values   map[string]any `json:"values"`
}

func (*SettingsChanged) messageType() string { return TypeSettingsChanged }

// CallKind names a host service a plugin may request.
type CallKind string

const (
	CallStateGet      CallKind = "state.get"
	CallStateSet      CallKind = "state.set"
	CallStateList     CallKind = "state.list"
	CallPanelOpen     CallKind = "panel.open"
	CallPanelClose    CallKind = "panel.close"
	CallNotify        CallKind = "notify"
	CallOutputContext CallKind = "output.context"
)

// HostCall is a request from the plugin. Params is left raw so that adding a
// call in a later minor version does not change this envelope.
type HostCall struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Call   CallKind        `json:"call"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (*HostCall) messageType() string { return TypeHostCall }

// HostReply answers exactly one HostCall.
type HostReply struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

func (*HostReply) messageType() string { return TypeHostReply }

// StatusState is a plugin's own account of its service, shown in the manager.
type StatusState string

const (
	StatusOK    StatusState = "ok"
	StatusBusy  StatusState = "busy"
	StatusError StatusState = "error"
)

// PluginStatus reports service state that is not visible from the process
// itself: a plugin can be running perfectly and still have nothing to show
// because a network fetch failed or a dependency is missing.
type PluginStatus struct {
	Type    string      `json:"type"`
	State   StatusState `json:"state"`
	Message string      `json:"message,omitempty"`
}

func (*PluginStatus) messageType() string { return TypePluginStatus }

// The call parameter and result payloads. They are separate types rather than
// maps so that both sides decode the same shape strictly.

// StateGetParams reads one namespaced value.
type StateGetParams struct {
	Key string `json:"key"`
}

// StateGetResult carries the stored value, if any.
type StateGetResult struct {
	Found bool            `json:"found"`
	Value json.RawMessage `json:"value,omitempty"`
}

// StateSetParams writes one namespaced value. A null value deletes the key.
type StateSetParams struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// StateListResult names the keys the plugin has stored.
type StateListResult struct {
	Keys []string `json:"keys"`
}

// PanelParams opens or closes a panel the manifest declared.
type PanelParams struct {
	// Entry is the manifest panel entry.
	Entry string `json:"entry"`
	// Output is the connector to open on. Empty means the focused output.
	Output string `json:"output,omitempty"`
	// Generation is Output's host identity. Zero means "current".
	Generation uint32 `json:"generation,omitempty"`
	// Instance ties the panel to the placement whose bar widget triggered it.
	Instance string `json:"instance,omitempty"`
}

// OutputContextParams reads the live connector a command should use.
type OutputContextParams struct {
	// Output names one connector. Empty means Niri's focused output.
	Output string `json:"output,omitempty"`
	// Generation, when set, must match the host's current identity for Output.
	Generation uint32 `json:"generation,omitempty"`
}

// OutputContextResult is the connector and generation the host currently holds.
type OutputContextResult struct {
	Output     string `json:"output"`
	Generation uint32 `json:"generation"`
}

// PanelResult names the view the host opened, so the plugin can close it.
type PanelResult struct {
	ViewID string `json:"view_id"`
}

// Urgency mirrors the notification specification's three levels.
type Urgency string

const (
	UrgencyLow      Urgency = "low"
	UrgencyNormal   Urgency = "normal"
	UrgencyCritical Urgency = "critical"
)

// NotifyParams sends one notification through the shell's own client, so a
// plugin does not need session-bus access of its own to be visible.
type NotifyParams struct {
	Summary   string         `json:"summary"`
	Body      string         `json:"body,omitempty"`
	Urgency   Urgency        `json:"urgency,omitempty"`
	Actions   []NotifyAction `json:"actions,omitempty"`
	TimeoutMS int32          `json:"timeout_ms,omitempty"`
}

// NotifyAction is one bounded notification button.
type NotifyAction struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// NotifyResult carries the served notification identity.
type NotifyResult struct {
	ID uint32 `json:"id"`
}
