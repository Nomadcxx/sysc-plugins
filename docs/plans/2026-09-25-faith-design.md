# Faith design

Date: 2026-09-25 · Baseline: `main` at `8fd1a24`, shell `main` at `f77226a` · Prior art: Noctalia
community `quranwidget` 1.0.1 (MezoAhmedII, MIT).

## Goal

A Christian devotional plugin for sysc-shell. A cross on the bar gives the user a prayer when
clicked. An attached panel shows a Scripture verse with commentary and cross-references. Every
text the plugin shows is Christian: Scripture, historic prayers, and Christian commentary.

The plugin is named Faith: `org.sysc.faith`, directory `plugins/faith`, executable
`sysc-plugin-faith`. The implementation plan is `2026-09-25-faith.md`.

## Decisions

1. **Both primary and secondary click give a prayer.** The cross's bar button declares
   `activate` and `pointer`; `activate` and a `secondary` pointer event each send one prayer
   through `notify`. The host also sends a `primary` pointer event on press before the
   release's `activate`; the plugin ignores it, so one left click is one prayer. A `middle`
   pointer event opens the verse panel. The tooltip names the
   current verse and says that middle click opens it.
2. **Prayers are bundled, not fetched.** No free prayer API exists. The plugin ships a fixed
   corpus as Go data, each entry carrying its title, text, tradition, and source.
3. **Public-domain prayers only.** Sources are Scripture (from a public-domain translation), the
   ancient creeds and hymns, the US 1928 and 1979 Books of Common Prayer (both public domain),
   and prayers whose authors died before 1900 or whose text is anonymous and published before
   1929. The 1662 Book of Common Prayer (Crown rights in the United Kingdom) and the Serenity
   Prayer (disputed copyright) are excluded.
4. **Ecumenical by default, one tradition setting.** `tradition` selects `ecumenical`
   (default), `catholic`, or `orthodox`. The ecumenical set is always in the pool; the other two
   add their own prayers to it rather than replacing it.
5. **No immediate repeats.** Prayers are drawn from a shuffle bag persisted in plugin state. The
   bag refills when empty and when the tradition setting changes.
6. **Scripture is bundled.** The full text of three public-domain translations ships inside the
   plugin: the Berean Standard Bible (BSB, default), the World English Bible (WEB), and the King
   James Version (KJV, 1769). Each is a gzip-compressed tab-separated file of about 1.3 MB,
   embedded with `go:embed` and decompressed only when that translation is selected. A generator
   builds the files from pinned sources: USFM for BSB and WEB from `HelloAOLab/bible-api`, and
   the KJV JSON from `scrollmapper/bible_databases`. All three use the 66-book Protestant canon.
   Verse counts come from each translation's own text, not from a shared table.
7. **Verse selection has three modes.** `verse_mode` is `pool` (default: random from a bundled
   list of 365 devotional references), `daily` (one pool entry per calendar date, the same for
   every user), or `bible` (uniform random over every verse in the translation). A reference may
   be a range, such as Romans 8:38–39.
8. **Offline first.** Verses, navigation, every verse mode, cross-references, and prayers need no
   network. Commentary is the only online feature. When it cannot be fetched, its section says so
   in one subtle line and nothing else changes.
9. **Three translations.** `translation` selects `BSB`, `WEB`, or `KJV`. The KJV is public domain
   everywhere except the United Kingdom, where Crown letters patent apply; the README says so.
10. **Commentary replaces tafsir.** With `show_commentary` on (the default), opening the panel
    fetches the verse's entry from Adam Clarke's commentary (public domain) through the Free Use
    Bible API (`/api/c/adam-clarke/{book}/{chapter}.json`). The plugin keeps fetched chapters in
    memory only. An entry can run to thousands of words, so the panel shows at most 40 wrapped
    lines and says when it has shortened one.
11. **Cross-references are new.** The reference has no equivalent. The five highest-voted
    OpenBible.info cross-references for each verse are bundled (the 2024-11-04 snapshot, CC BY)
    and appear as buttons; activating one moves the panel to it. The section carries a subtle
    "OpenBible.info" credit and the README credits it too.
12. **The plugin wraps text itself.** The protocol has no text wrapping and the host measures
    text at eight pixels per byte. Verse, prayer excerpt, and commentary are broken into lines at
    `floor(content width / 8)` bytes on word boundaries, one text node per line. The panel body
    is a `list`, the protocol's only scrolling container.
13. **A new `cross` glyph in the shell's icon font.** The shell's catalogue has no cross. A Latin
    cross SVG (`internal/render/icons/svg/cross.svg`) is appended to `GLYPHS` in
    `internal/render/icons/build.py` at the next private-use codepoint, and `sysc-icons.ttf` is
    rebuilt. This is the only shell change, and the plugin's shell pin moves past it.
14. **No desktop widget.** sysc-shell's plugin floating surfaces are Top/Overlay-layer sticky
    windows, not desktop widgets, so the quranwidget's desktop card has no port. A plugin-owned
    Bottom-layer surface needs its own shell design.
15. **Notification buttons are omitted.** The `notify` call accepts actions, but no protocol
    message returns an invoked action to the plugin, so an "Another prayer" button would do
    nothing. A second click is the way to get another prayer.

## Surfaces

### Bar

A single-child row: the `cross` icon button, named "Faith: click for a prayer". An optional
`show_reference` setting (default off) appends the current verse reference as subtle text, making
the row two children with the icon first.

### Tooltip

A column: the reference (accent, bold), the first two wrapped lines of the verse, and a subtle
caption "Middle-click to read".

### Prayer notification

`summary` is the prayer's title ("The Lord's Prayer"); `body` is its full text with a final line
naming the source ("Matthew 6:9–13, BSB"). Urgency is `low`. Timeout comes from the
`prayer_seconds` setting (default 30; 0 uses the notification service's default).

### Panel

Attached, 440×460 in the manifest. From the top:

1. Header row: reference (title, accent) and translation abbreviation (caption, subtle, pinned
   end).
2. Control row: previous verse, next verse, new verse, and "Read chapter" in the browser
   (`xdg-open` to biblehub.com). It stays above the scrolling list so the controls never scroll
   away.
3. A `list` holding the verse text (wrapped); then a separator and "Commentary" with the entry
   (wrapped, at most 40 lines) or a one-line status; then a separator and "See also" with up to
   five cross-reference buttons and the OpenBible.info credit.

The previous and next buttons are disabled at Genesis 1:1 and Revelation 22:21. Every layout is
checked by `plugin/lint` at 440×460.

## Settings

| Key | Type | Default | Meaning |
|---|---|---|---|
| `tradition` | select | `ecumenical` | Adds Catholic or Orthodox prayers to the ecumenical pool. |
| `verse_mode` | select | `pool` | `pool`, `daily`, or `bible`. |
| `refresh_minutes` | int | `30` | New verse interval in `pool` and `bible` modes; 0 disables. Ignored in `daily`. |
| `translation` | select | `BSB` | `BSB`, `WEB`, or `KJV`. |
| `show_commentary` | bool | `true` | Fetch Adam Clarke's commentary when the panel opens. |
| `show_reference` | bool | `false` | Show the verse reference beside the cross. |
| `prayer_seconds` | int | `30` | Prayer notification timeout, 0–300. |

## State

Plugin state holds the current reference and the prayer shuffle bag. Scripture needs no cache,
and commentary is held in memory only.

## Prayer corpus

Ecumenical: the Lord's Prayer, the Apostles' Creed, the Nicene Creed, the Gloria Patri, the
Doxology ("Praise God, from whom all blessings flow", Ken, 1674), the Aaronic Blessing
(Numbers 6:24–26), the Grace (2 Corinthians 13:14), the Jesus Prayer, the Prayer of St. Francis
(1912), St. Patrick's Breastplate (C. F. Alexander's 1889 translation, excerpt), a selection of
BCP collects (morning, evening, for guidance, for peace, for quiet confidence, for the sick,
grace before meals), and short prayers from the Psalms.

Catholic adds the Hail Mary, the Memorare, the Angelus versicles, the Anima Christi, and the Act
of Contrition. Orthodox adds the Trisagion, "O Heavenly King", and the Prayer of St. Ephrem.

The plan lists every entry with the edition its text is taken from.

## Non-goals

A desktop card, audio playback, the deuterocanonical books, a liturgical calendar, reading
plans, and languages other than English. Each is a later design if wanted.
