package source

import (
	"context"
	"os/exec"
	"syscall"
)

// scopeRunner starts a program in its own systemd user scope. A variable so a
// test can take it away.
var scopeRunner = "systemd-run"

// Detach starts a desktop program and returns without waiting for it.
//
// A game started through xdg-open, Lutris and Steam lives as long as the game.
// Waiting on it stalled the plugin's event loop until the game and Steam had
// exited, and as the plugin's child it lived in the shell's service, so
// restarting the shell killed the game. The program runs in its own user
// scope, the way a desktop launcher starts an app; without a reachable user
// manager it starts in a new session instead. Either way it is launched once.
func Detach(_ context.Context, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if scopesWork() {
		cmd = exec.Command(scopeRunner, append([]string{"--user", "--scope", "--collect", "--quiet", "--", path}, args...)...)
	} else {
		cmd = exec.Command(path, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap; the exit status belongs to the app
	return nil
}

// scopesWork asks the user manager for a throwaway scope. It fails within
// milliseconds when systemd-run is missing or the user bus is unreachable.
func scopesWork() bool {
	runner, err := exec.LookPath(scopeRunner)
	if err != nil {
		return false
	}
	return exec.Command(runner, "--user", "--scope", "--collect", "--quiet", "--", "true").Run() == nil
}
