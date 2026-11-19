// lan-launcher runs a command as its child so the command gets macOS Local
// Network access. macOS 15 ties that permission to the exact binary that was
// approved, and ./serve rebuilds awh on every start, so awh itself would need
// approving again after each build; from tmux macOS can't ask and silently
// answers "no route to host". This launcher is built and approved once
// (scripts/make-lan-launcher) and never rebuilt; its children, awh and the
// ffmpeg processes awh starts, inherit its permission.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: lan-launcher command [args...]")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// Ctrl-C reaches the child directly through the terminal's process group;
	// TERM and HUP (tmux kill-session, closing the pane) are sent on.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "lan-launcher:", err)
		os.Exit(127)
	}
	go func() {
		for s := range sigs {
			if s != syscall.SIGINT {
				_ = cmd.Process.Signal(s)
			}
		}
	}()

	err := cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			os.Exit(128 + int(ws.Signal()))
		}
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lan-launcher:", err)
		os.Exit(1)
	}
}
