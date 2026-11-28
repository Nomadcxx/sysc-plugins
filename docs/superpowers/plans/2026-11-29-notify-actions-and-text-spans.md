# Plugin Notification Actions and Text Spans Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make buttons on plugin notifications appear and report clicks back to the owning plugin (sysc-shell protocol minor 29). Let plugin text nodes carry coloured spans (minor 30).

**Architecture:** Plugin toasts are published through sysc-notify's *producer* protocol, which has no actions today. Change A adds actions to `protocol.ProducerRequest` in sysc-notify. sysc-shell then passes them through, remembers which plugin owns each resulting notification id, and forwards clicks on those ids to that plugin as a new `notify.action` message. Change B adds `Spans` to `v1.Node` text nodes. Layout measures the concatenated text exactly as before, and only the painter colours each run.

**Tech Stack:** Go; repos `github.com/Nomadcxx/sysc-notify` and `github.com/Nomadcxx/sysc-shell`.

**Spec:** `docs/superpowers/specs/2026-11-29-bitsub-design.md` §11 (in sysc-plugins). The consumer is Task 14 of `docs/superpowers/plans/2026-11-29-bitsub.md`.

## Global Constraints

- Never build or commit from `~/sysc-shell` or `~/sysc-notify` directly. Make detached worktrees from `origin/main` (`git worktree add -b <branch> ~/worktrees/<name> origin/main`).
- Commit messages: the same banned-word hook as sysc-plugins (no agent/bot/claude/codex/llm/AI trailers).
- sysc-shell current `ProtocolMinor = 28` on origin/main. A is minor **29** and B is minor **30**, landing in that order, one PR each. If main has moved past 28 when you start, take the next free numbers and update the consumer plan's Task 14 to match.
- Old plugins and old hosts keep working: hosts ignore unknown node fields from newer plugins only where noted, and plugins below minor 29 never receive `notify.action`.
- Deploys follow the owner's sysc-shell deploy rule (`scripts/deploy --host desktop|laptop`, ancestor check, rollback copy). Deploying is not part of the coding tasks. Ask the owner first.
- Go command caps: `-p 2`, race per package.

## Review Focus

1. **A click on a non-plugin notification** (e.g. a Firefox toast with the same action key "keep:…"): it's never routed to any plugin. Pinned in Task A2.
2. **The plugin restarted or disabled between notify and click:** the click is dropped silently with no panic, and the service still closes the toast. Pinned in Task A2.
3. **A plugin negotiating minor 28 sends actions:** they're shown (the service accepts them), but no `notify.action` is ever delivered to that plugin. Pinned in Task A2.
4. **Spans with multi-byte block glyphs** (`▒▓█▄▀`): the measured width is identical to the same row as plain `Text`, so a panel that linted before still lints. Pinned in Task B1.
5. **`Text` and `Spans` both set, or a span with an unknown tone:** `v1.Validate` rejects it with a path-named error. Pinned in Task B1.

---

### Task A1: sysc-notify — actions on producer notifications

**Files (sysc-notify worktree):**
- Modify: `protocol/types.go` (`ProducerRequest`), `protocol/validate.go` (`ProducerRequest.Validate`), `internal/state/owner.go` (`publishProducer`), and wherever `notify.Candidate` → `protocol.Notification` copies actions (find with `git grep -n "Actions" internal/`)
- Test: `protocol/types_test.go` or `protocol/validate_test.go`, `internal/state/owner_test.go`

**Interfaces:**
- Produces: `ProducerRequest.Actions []Action `json:"actions,omitempty"``. Validation reuses `Action.Validate`, `MaxActionPairs` (6) and the duplicate-key check used for notifications. `publishProducer` copies the actions into the candidate, so the resulting `protocol.Notification.Actions` is set and `invokeAction` (owner.go) accepts those keys, publishes its `ActionEvent` and closes the non-resident producer toast.

- [ ] **Step 1: Write failing tests**: `TestProducerRequestValidatesActions` (7 actions → error; a duplicate key → error; an empty label → ok), `TestProducerPublishCarriesActions` (publish with `[{keep:x,Keep}]` → the record's notification has that action), `TestInvokeActionOnProducerNotification` (invoke `keep:x` → `ActionEvent{ID,Key}` published and the record closed with `CloseDismissed`; invoke an unknown key → error, record kept).
- [ ] **Step 2: Run** `go test -p 2 ./protocol/ ./internal/state/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command plus `go test -race ./internal/state/`. Expected: PASS.
- [ ] **Step 5: Commit, open a PR, and after merge tag the next release** (the latest tag is listed by `git tag --sort=-v:refname | head -1`. Use the next minor, e.g. `v0.3.0`, and confirm the number with the owner). `git commit -m "feat(protocol): actions on producer notifications"`

---

### Task A2: sysc-shell — route plugin notification clicks (minor 29)

**Files (sysc-shell worktree):**
- Modify: `go.mod`/`go.sum` (sysc-notify → the A1 tag), `plugin/v1/message.go` (`ProtocolMinor = 29`, new message), `internal/shell/notifications.go` (`PluginNotify`, `publishToast`), `internal/shell/toasthost.go` (`invoke`), `internal/shell/panelhost.go` (drawer action path near the `notify:%d:action:` handling, ~L4490), `internal/plugin/` host delivery (where `InputEvent`/`SettingsChanged` are sent to a plugin runtime; find with `git grep -n "TypeSettingsChanged" internal/plugin`)
- Test: `internal/shell/notifications_test.go` (or a new `plugin_notify_actions_test.go`), `plugin/v1` message round-trip test, and an `internal/plugin` delivery test

**Interfaces:**
- Produces in `plugin/v1`: `const TypeNotifyAction = "notify.action"`; `type NotifyActionEvent struct{ Type string `json:"type"`; ID uint32 `json:"id"`; Key string `json:"key"` }` with `messageType() string { return TypeNotifyAction }`, decodable by `Client.Recv`. Document it in the protocol history of sysc-plugins `docs/writing-plugins.md` later (consumer side).
- Produces in shell:
  - `publishToast(key, summary, body string, urgency protocol.Urgency, timeoutMS int32, actions []protocol.Action) (uint64, error)`. Existing callers pass `nil`.
  - `PluginNotify` gains the calling plugin's identity. Extend the `Notify` seam in `internal/plugin/hostcall.go` from `func(context.Context, v1.NotifyParams)` to `func(context.Context, PluginRef, v1.NotifyParams)`, where `PluginRef` is whatever the host already uses to address a plugin instance for delivery. It maps `v1.NotifyAction` → `protocol.Action`, publishes, and records `pending[requestID] = ref`.
  - When the producer-publish **reply** for that request id arrives (the `protocol.Reply.ID` is the notification id; find where `notifyclient` surfaces replies to `applyNotify`, or add a reply message if it doesn't surface them), move it to `owned[notificationID] = ref`. Delete entries when the notification closes (closed delta), and cap the map at 256 entries, evicting the oldest.
  - In `toastHost.invoke` and the drawer action path: if `owned[id]` exists and the plugin negotiated ≥ 29 and is running, deliver `NotifyActionEvent{ID:id, Key:key}` to it. In **every** case, still send `protocol.CommandAction` to the service as today, so the toast closes. A body click sends key `default`.
- Remove the `ponytail: v1 Actions are dropped` comment.

- [ ] **Step 1: Write failing tests**: `TestPluginNotifyPassesActions` (a recording producer sender sees `Producer.Actions == [{keep:x,Keep}]`), `TestPluginActionRoutesToOwner` (notify from plugin P, simulate the reply id 42, invoke(42,"keep:x") → P receives `NotifyActionEvent{42,"keep:x"}` and the service still gets `CommandAction`), `TestForeignNotificationNeverRoutes` (invoke on id 7, which isn't owned → no plugin delivery), `TestOwnerBelowMinor29GetsNothing`, `TestStoppedPluginDropsSilently`, `TestOwnedMapCleanedOnClose`, `TestNotifyActionEventRoundTrip` (encode → `Client.Recv` decodes the type).
- [ ] **Step 2: Run** `GOWORK=off go test -p 2 ./plugin/v1/ ./internal/plugin/ ./internal/shell/`. Expected: FAIL. Use `keepInvalidationsDrained` in shell tests, or they hang on `r.mu`.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command plus `-race` for `./internal/shell/` alone. Expected: PASS.
- [ ] **Step 5: Commit and open a PR.** `git commit -m "feat(plugin): deliver notification action clicks to the owning plugin"`

---

### Task B1: sysc-shell — coloured text spans (minor 30)

**Files (sysc-shell worktree, branched after A2 merges):**
- Modify: `plugin/v1/node.go` (field, validation, `ProtocolMinor = 30` in `message.go`), `internal/plugin/view.go` (the `case v1.KindText:` conversion, ~L377), `internal/ui` node type (add runs), and the text painter in `internal/render` (find where `ui.KindText` is drawn with a tone colour: `git grep -n "KindText" internal/render internal/ui`)
- Test: `plugin/v1/node_test.go`, `internal/plugin/view_test.go`, a render test next to the existing text paint tests, `plugin/lint` test

**Interfaces:**
- Produces in `plugin/v1`: `type TextSpan struct{ Text string `json:"text"`; Tone Tone `json:"tone,omitempty"`; Bold bool `json:"bold,omitempty"` }`; `Node.Spans []TextSpan `json:"spans,omitempty"``; `const MaxTextSpans = 64`.
- Validation: `Spans` only on `KindText`; `Text` must be empty when `Spans` is set; 1–64 spans; each tone in the existing tone set; the summed bytes ≤ `MaxTextBytes`; every span is valid UTF-8.
- Conversion: `ui.Node.Text` = the concatenation (so measurement, wrapping, clipping and lint use the existing path unchanged), plus `ui.Node.Runs []ui.TextRun{Start, End int /* byte offsets */; Tone; Bold}`. The painter draws each run with its tone's colour. Nodes without runs paint exactly as before.

- [ ] **Step 1: Write failing tests**: `TestSpansValidation` (spans on a row → error; Text+Spans → error; 65 spans → error; tone "purple" → error; a valid art row → nil), `TestSpansMeasureLikeText` (the bitsub art row as spans vs as Text → `plugin/lint.Tree` gives identical findings and the measured node width is equal), `TestConvertSpansSetsRuns` (byte offsets cover the text with no gaps), `TestPainterColoursRuns` (render a 2-run node to an image, then sample one pixel in each run: accent colour vs subtle colour per `theme.NamedPalette`).
- [ ] **Step 2: Run** `GOWORK=off go test -p 2 ./plugin/v1/ ./plugin/lint/ ./internal/plugin/ ./internal/render/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit and open a PR.** `git commit -m "feat(plugin): coloured text spans on text nodes"`

---

### After merge

- [ ] Ask the owner before deploying the shell to archPC and the laptop with `scripts/deploy`. Report `vcs.revision` and `vcs.modified`.
- [ ] Unblock Task 14 in the bitsub plan (bump the sysc-shell pin in sysc-plugins).
