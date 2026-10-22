# Privacy audit

This state-only plugin runs `privacybit audit --snapshot` on a five-minute
interval and publishes a bounded summary for sysc-shell's Control Centre →
Privacy page. The host renders the page and owns its separate **Run scan**
action.

Install `privacybit` separately to see audit results. Without it, the plugin
still starts and publishes an explicit unavailable state. The plugin does not
require elevated privileges.
