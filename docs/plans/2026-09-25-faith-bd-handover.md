# Faith bd handover

Date: 2026-09-25. Branches: sysc-plugins and sysc-shell `claude/compassionate-hypatia-i402xp`.
Plan: `2026-09-25-faith.md`. Design: `2026-09-25-faith-design.md`.

This file exists to put the Faith work into bd. Once every item below is in bd, delete this
file and its register row (AGENTS.md handover rule).

## Completed work: suggested issues to create and close

| Task | Suggested bd title | Commit | Close reason |
|---|---|---|---|
| S1 | Cross and menu_book glyphs for the Faith plugin | sysc-shell `5260c75` | `TestCrossGlyphIsALatinCross` and `TestFaithPanelIconsAreInTheSubset` pass; every existing glyph in the two fonts is byte-identical. |
| 0 | Faith design and plan | `5f7ced7`, `006bc3c`, `cedf611`, `268f0d1`, `2f79040` | Docs registered. |
| 1 | Faith: scaffold the plugin | `647f0d0` | `validate-manifests`, `make build`, and `catalog-validate` pass. The shell pin moved to `5260c75`. |
| 2 | Faith: book table and references | `6ba7ce1` | `books_test.go`. |
| 3 | Faith: data generator and bundled Scripture | `ea82ac3`, `257c119` | Generator tests pass and output is byte-identical across runs. BSB has 31,086 verses (the KJV's 31,102 less the 16 disputed verses), WEB 31,098, and KJV 31,102. |
| 4–6 | Faith: store, pool, cross-references | `9aac774` | The pool is 365 references that resolve in all three translations. John 3:16's best cross-reference is Romans 5:8. |
| 7 | Faith: commentary client | `10dde77` | httptest coverage for decoding, 404, the size cap, cancellation, and the cache bound. |
| 8 | Faith: prayer corpus and shuffle bag | `e2b3357` | 33 prayers. The bag deals each prayer once per round, never back to back. |
| 9 | Faith: wrapping and views | `e1f427d` | `TestViewsFitTheirHostSlots` runs `plugin/lint` at bar, tooltip, and 440×460 for every commentary state. |
| 10 | Faith: session and settings | `5781f6a` | A left click prays once (the primary press is ignored), the panel refresh pauses, and daily mode turns at midnight. |
| 11 | Faith: protocol loop | `ade6fc7` | The in-process fake-host tests pass five times under `-race`. |
| 12 | Faith: integration gate | `46d3c72` | `TestPluginFaithGate` passes three times under `-race`. |
| 13 | Faith: attribution | this commit | README attribution; `make validate` and `make catalog-validate` pass. |

## Open items: create as open issues

1. **Merge the shell glyphs and re-pin (gate).** Merge sysc-shell `5260c75` to `main`, re-pin
   sysc-plugins to the merge commit, and tag `faith-v0.1.0` only after a shell release carries
   it. An older shell refuses the bar: an unknown icon fails conversion.
2. **Live Niri check (plan Task 13 step 3).** This has not been run. Check that:
   - the cross renders at bar size;
   - a left click gives one prayer, and a right click gives one;
   - a middle click opens the panel;
   - commentary loads online and says it is unavailable offline;
   - "Read chapter" opens the right biblehub.com page in BSB, WEB, and KJV (the URL template
     is unverified because the build sandbox could not reach biblehub.com);
   - `niri msg -j layers` shows the panel.
3. **Commentary endpoint against the live API.** The client matches the documented shape of
   `/api/c/adam-clarke/{BOOK}/{chapter}.json`, but the sandbox could not reach
   `bible.helloao.org`.
4. **Traditional prayer texts.** The Memorare, Angelus, Anima Christi, St. Michael, and the three
   Orthodox prayers use their long-standing public-domain English wording. Check them against a
   scanned edition (the Raccolta, Hapgood's 1906 Service Book).
5. **Shell tray integration tests in the sandbox.** 17 tray tests fail in sysc-shell's
   `tests/integration` with and without `5260c75` (the same on `f77226a`). They need a run on
   the owner's machine before the merge.
6. **Minimum shell version in manifests (shell).** A manifest cannot say it needs a shell that
   carries a given glyph. Consider a field.
7. **Observation: `Client.Handshake` answers protocol 1.0.** `plugin/v1`'s `Handshake` always
   selects `{1, 0}` whatever the host supports. Every plugin shares this. Check whether the host
   then drops minor-2+ presentation (subtle and accent tones, `Size`, separators).

## Failures in the build sandbox that predate this work

Each failed identically at `8fd1a24`, before any Faith commit:

- `plugins/notes`: `TestStoreAtomicSaveSurvivesFailure`, `TestSessionDirtyAutosaveFlushAndSaveError`
  (the sandbox runs as root, so permission-denied paths succeed).
- `plugins/timer`: `TestTransitionSoundsExist` (no freedesktop sound theme is installed).
- `tests/integration`: `TestPluginMiniDockerGate` and `TestPluginNotesGateExternalChangeAndReadOnly`
  fail on every run. `TestPluginKDEConnectGateServesViewsAndSurvivesInput` is flaky under
  `-race`, failing 2 of 4 runs at the old pin and 3 of 4 at the new one.

## Plan deviations

- The panel's controls sit in a fixed row above the scrolling list, not after the verse; the
  design is amended.
- A mutex-guarded writer replaces the plan's `sender` mutex (plan section 2, item 2, amended).
- Prayer sources marked "Traditional English" replace the Raccolta and Hapgood attributions
  that could not be checked (plan section 3, amended).
