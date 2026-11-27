// Package childproc starts detached helper processes without leaving zombies
// behind.
package childproc

import "os/exec"

// Start launches cmd and reaps it in the background. The caller keeps the
// child's session settings, but must not call Wait itself.
func Start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}
