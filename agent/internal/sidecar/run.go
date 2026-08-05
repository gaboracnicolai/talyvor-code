package sidecar

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Stdio is the child's terminal. It is passed through untouched so an interactive TUI — which is
// what Claude Code is — behaves exactly as it does when run directly.
type Stdio struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Run starts the sidecar, runs the command against it, and returns the command's exit code.
//
// ⚠ THE PROXY NEVER OUTLIVES THE CHILD. Every return path below goes through the deferred Close,
// including the error paths, because an orphaned proxy holding a Lens API key is a credentialed
// listener nobody knows is running. run_test.go proves this by DIALLING the port afterwards on
// three separate paths — clean exit, crash, and a real SIGINT to a real process tree.
//
// started, when non-nil, is called once the listener is up and before the child starts. It exists
// so the caller can print the port and so tests can capture it.
func Run(cfg Config, command []string, io_ Stdio, started func(*Sidecar)) (int, error) {
	if len(command) == 0 {
		return 1, errors.New("no command given: try `talyvor-code exec -- claude`")
	}
	// ⚠ REFUSED BEFORE ANYTHING STARTS. A proxy that cannot authenticate to Lens would turn every
	// request into a 401 and read to the developer as the model being broken.
	if cfg.LensAPIKey == "" {
		return 1, errors.New("no Lens API key configured: set TALYVOR_LENS_API_KEY " +
			"(the sidecar authenticates to Lens on the child's behalf, so the child needs no key of its own)")
	}

	s, err := Start(cfg)
	if err != nil {
		return 1, err
	}
	defer func() { _ = s.Close() }()

	// ⚠ THE HANDLER IS INSTALLED BEFORE THE CHILD EXISTS, NOT AFTER.
	//
	// We ignore the interrupt rather than acting on it: the child has already received the same
	// Ctrl-C from the terminal, and if we exited on it we would tear the proxy down mid-request
	// while the child was still shutting down and finishing its last call. We let the child decide
	// when it is finished, and the deferred Close runs the moment it is.
	//
	// Ordering matters more than it looks. Registering this AFTER cmd.Start leaves a window in
	// which a Ctrl-C kills us on the default disposition while the child is already running —
	// precisely the orphaned-child case this design exists to prevent. It is also invisible in
	// testing unless you look for it: the window is milliseconds wide, and a test that signals
	// during it passes for the wrong reason, which is how this ordering was found.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		for range sigs {
		}
	}()

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = io_.In, io_.Out, io_.Err
	cmd.Env = s.ChildEnv(os.Environ())
	// ⚠ NO SEPARATE PROCESS GROUP, DELIBERATELY. The child stays in our foreground group so the
	// terminal delivers Ctrl-C to it directly, exactly as if it had been run without us. Isolating
	// it would make us responsible for relaying every signal and would break the TUI's job control
	// — a background process group reading the terminal gets stopped by SIGTTIN.

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("could not start %s: %w", command[0], err)
	}

	// Announced only once everything is live: the proxy is listening, the handler is installed and
	// the child is running. Anything earlier would report readiness the process cannot yet honour.
	if started != nil {
		started(s)
	}

	err = cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// A signalled child has no exit code of its own; the shell convention of 128+signal keeps
		// "it was killed" distinguishable from "it returned an error".
		if code := exitErr.ExitCode(); code >= 0 {
			return code, nil
		}
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return 1, nil
	}
	return 1, err
}
