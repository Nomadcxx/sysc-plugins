# Lid Guard

Lid Guard controls one transient systemd user service, `noctalia-lid-guard.service`. The service runs `systemd-inhibit` for `handle-lid-switch` while it waits. Closing the lid will not trigger logind's lid-switch action while the service is active.

The [Noctalia Community Lid Guard](https://github.com/noctalia-dev/community-plugins/tree/main/lid-guard) plugin uses the MIT license. This port preserves its behavior.

The inhibitor covers lid-close handling only. Idle timeout, manual suspend, and other power actions still work. Lid Guard does not edit system or logind configuration and does not start automatically.

Use the bar button to toggle the service. Right-click it to refresh status and middle-click it to open the detail panel. The Control Center Home page has the same toggle. The panel shows the observed systemd state, an Enable or Disable action, and Refresh.

From the command line, send one of the declared service actions:

```sh
sysc-shell ipc plugin.action '{"plugin_id":"org.sysc.lid-guard","service_id":"lid-guard","action_id":"toggle"}'
```

Replace `toggle` with `enable`, `disable`, `refresh`, or `status`. The shell queues the request and the plugin reports the observed result asynchronously.

The service can outlive the plugin process. To stop it if the plugin is unavailable, run:

```sh
systemctl --user stop noctalia-lid-guard.service
```

Lid Guard needs `systemctl`, `systemd-run`, `systemd-inhibit`, and `sleep` in `PATH`, plus a running systemd user service manager. The plugin keeps running when one of these is unavailable so it can show the problem and the recovery command.

The service state comes from systemd. Lid Guard checks it at startup, after actions, and every five seconds. It stores no second copy of the active state.
