# Attribution

Where each plugin's behavior comes from:

| Plugin | Origin |
|---|---|
| Screen Recorder | extracted from sysc-shell |
| Notes | extracted from sysc-shell |
| Timer | extracted from sysc-shell, with deltas from the noctalia timer |
| World Clock | extracted from sysc-shell, redesigned in 2.0.0 |
| Calendar | port of the built-in sysc-shell clock-panel calendar |
| GitHub Notifications | port of noctalia community plugin |
| Mini Docker | port of noctalia community plugin |
| Wallpaper Depth | port of noctalia official plugin |
| Phone Connect | port of DMS DankKDEConnect |
| AI Usage | new; patterns from noctalia ai-usagebar and DMS usage widgets |
| Faith | new; behavior from the noctalia quranwidget community plugin |
| Cat | port of noctalia cat and the DMS Cat Widget |
| ProtonVPN | new; `protonvpn` CLI backend, prior art from Noctalia, DMS and the ProtonVPN GTK app |
| Games | new; reference is the lutrisLauncher DMS plugin (hthienloc/dms-plugins) |

Plugins marked "port of noctalia ..." are Go rewrites of behavior originally
implemented by [noctalia-dev](https://github.com/noctalia-dev) for Noctalia v5
(`plugin.toml` + Luau), under the MIT license. sysc-shell does not claim
runtime compatibility with Noctalia; only behavior is ported. Plugins marked
"port of DMS ..." are Go rewrites of behavior originally implemented by
Avenge Media for DankMaterialShell's DankKDEConnect plugin
(dms-plugin-registry #386), under the MIT license; runtime compatibility
with DMS is not claimed or preserved.

Cat ports the behaviour of noctalia's `cat` community plugin (DotNetRob) and
the DMS Cat Widget (xi-ve/cat-dms, dms-plugin-registry #562): a bar cat
whose pace follows CPU load. Neither reference's artwork is used. The cat is
forty original poses in sysc-shell's own icon font -- walking, galloping,
sitting, grooming, scratching, stretching and sleeping -- and the shell
animates them on its own frame clock (protocol minor 8 sprite cycles), so the
plugin sends a message per act, never per pose. It reads `/proc/stat` only.

AI Usage is a new plugin built from the patterns in its prior-art research
(docs/plans/2026-09-19-aiusage-research.md), including the owner's own
noctalia ai-usagebar plugin. Its codex session-file collector reads session
logs only — never authentication files, never the network — and pasted API
keys live in the host's own settings store and are sent only to their
provider's API.

Faith is a new plugin whose behavior follows MezoAhmedII's Quran Widget
(noctalia community plugins, MIT); no code is shared. Its bundled data is
listed with commits and checksums in `plugins/faith/data/SOURCES.md`:

- The Berean Standard Bible (public domain since 2023) and the World English
  Bible (public domain), from the USFM in
  [HelloAOLab/bible-api](https://github.com/HelloAOLab/bible-api).
- The King James Version (1769), from
  [scrollmapper/bible_databases](https://github.com/scrollmapper/bible_databases).
  It is public domain except in the United Kingdom, where Crown letters patent
  apply.
- Cross-references from [OpenBible.info](https://www.openbible.info/labs/cross-references/),
  CC BY, by way of the same scrollmapper mirror. The five highest-voted
  references per verse are kept.
- Prayers from the 1928 and 1979 Books of Common Prayer (US editions, public
  domain), the 1891 Baltimore Catechism, Thomas Ken (1674), C. F. Alexander's
  1889 translation of St. Patrick's Breastplate, and traditional public-domain
  English wordings; Scripture prayers read the user's chosen translation.

Adam Clarke's commentary (public domain) is not bundled. When the panel is
open, it is fetched from the [Free Use Bible API](https://bible.helloao.org),
the only network request the plugin makes. Nothing about the user is sent.
