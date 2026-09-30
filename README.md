# sysc-plugins

The official plugins for [sysc-shell](https://github.com/Nomadcxx/sysc-shell). Each one is a small Go
program the shell starts and talks to over a pipe, so a plugin that crashes takes nothing else down with
it.

## Plugins

| Plugin | What it does | Needs |
|---|---|---|
| **AI Usage** | AI coding-assistant quotas in the bar, with a detail panel and threshold alerts | |
| **Calendar** | Read-only calendar with event views and details | evolution-data-server |
| **Cat** | A bar cat that walks and gallops with CPU load, and grooms, stretches and naps when the machine is idle | |
| **Faith** | A cross in the bar that offers a prayer, and a Scripture panel with cross-references and commentary | |
| **Games** | Lutris game deck: launch games, see what's running, session history, cover art | Lutris |
| **GitHub Notifications** | Triage unread GitHub notifications and contribution activity from the panel | `gh` |
| **Mini Docker** | The Docker whale in the bar, with a panel to manage containers | `docker` |
| **Moonbit** | System cleaner: scan and clean progress from the bar, with a category review panel | `moonbit` |
| **Notes** | Quick notes in a bar panel, autosaved as markdown files to a folder | |
| **Phone Connect** | A paired phone's battery, notifications and recent photos, over KDE Connect | `kdeconnect-cli` |
| **Pomodoro Timer** | Work and break sessions in the bar | |
| **ProtonVPN** | Quick connect, server picker, split tunnel and protection status | `protonvpn` |
| **Screen Recorder** | Record the screen or a window, with optional audio and a replay buffer | `gpu-screen-recorder` |
| **Wallpaper Depth** | Depth masks for image wallpapers, so scenery can sit in front of the shell's centred clock | `python3` |
| **World Clock** | Labelled clocks for other cities in the bar and panel | |

Phone Connect is still a skeleton. AI Usage, Faith and Games are first releases.

## Installation

**Requires:** sysc-shell, and whatever a plugin lists under **Needs**.

### From the shell

Released plugins show up in the shell's plugin manager, which installs them from this repository's
catalog:

```bash
sysc-shell ipc panel.toggle '{"panel":"plugin"}'
```

Only Pomodoro Timer has a release so far. The rest install from source.

### Build from Source

**Requires:** Go 1.26.4+ and `make`.

```bash
git clone https://github.com/Nomadcxx/sysc-plugins
cd sysc-plugins
make install
```

`make install` builds every plugin and symlinks each directory into
`$XDG_CONFIG_HOME/sysc-shell/plugins` (usually `~/.config/sysc-shell/plugins`) under its manifest id
(for example `org.sysc.timer`). Because they are symlinks, the clone has to stay where it is. Then
open the plugin manager and enable the ones you want.

To update, pull and run `make install` again.

> World Clock 2.0.0 saves its zones in a new format. Going back to 1.2.0 loses the zone list the next
> time it saves.

## Writing plugins

- [Writing a plugin](docs/writing-plugins.md): layout, the manifest, and what the host will reject
- [Plugin UI rules](docs/plugin-ui-rules.md): sizes and layout checks every view must pass
- [Publishing](docs/publishing.md): per-plugin release tags, the catalog, and running your own plugin
  source

Any git repository can be a plugin source. Copy the catalog tooling from here and keep a
`catalog.json` at the root of the default branch.

## Development

```bash
make build      # build every plugin into plugins/<dir>/bin/
make test       # go test -race ./...
make validate   # check every manifest.json
make catalog-validate
```

## Attribution

Several plugins port behavior from Noctalia and DankMaterialShell plugins. No runtime compatibility
with either is claimed. Faith bundles public-domain and CC BY texts. Sources and licenses are listed
in [ATTRIBUTION.md](ATTRIBUTION.md).

## License

MIT

---

<a href="https://github.com/Nomadcxx"><img src="https://raw.githubusercontent.com/Nomadcxx/Nomadcxx/main/assets/rama-mark.svg" height="22" alt="RAMA"></a> — terminal-native tooling for the linux desktop.
[More projects →](https://github.com/Nomadcxx) · [Sponsor](https://github.com/sponsors/Nomadcxx) ❤️
