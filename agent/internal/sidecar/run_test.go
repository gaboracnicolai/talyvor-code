package sidecar

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ⚠ IT MUST DIE WITH THE CHILD — normal exit, crash, and Ctrl-C.
//
// An orphaned proxy holding a Lens API key is worse than no proxy: it is a billable, credentialed
// listener that nobody knows is running. Every one of these tests therefore ends by DIALLING the
// port, because "we called Close" is not evidence that the socket is gone.

func lensStub(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	return srv.URL
}

// childReady is what the child under test echoes once it has run its own first line.
const childReady = "CHILD-READY"

// portIsClosed reports whether nothing accepts on addr any more.
func portIsClosed(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return true
	}
	_ = c.Close()
	return false
}

// portClosesWithin polls, because a listening socket belonging to a process that has just been
// reaped is torn down by the kernel a few milliseconds after wait() returns — measured on this
// machine. Polling does not weaken the assertion: a genuinely orphaned proxy holds its port for as
// long as it lives, so it fails this just as hard, only later.
func portClosesWithin(d time.Duration, addr string) bool {
	deadline := time.Now().Add(d)
	for {
		if portIsClosed(addr) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTheProxyDiesWhenTheChildExitsNormally(t *testing.T) {
	var addr string
	code, err := Run(Config{LensURL: lensStub(t), LensAPIKey: "k", Log: io.Discard},
		[]string{"sh", "-c", "exit 0"},
		Stdio{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard},
		func(s *Sidecar) { addr = strings.TrimPrefix(s.BaseURL(), "http://") })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !portClosesWithin(5*time.Second, addr) {
		t.Errorf("the proxy is still listening on %s after the child exited", addr)
	}
}

// ⚠ THE CHILD'S EXIT CODE IS THE TOOL'S EXIT CODE. `talyvor exec -- npm test` is worthless in CI
// if a failing child reports success.
func TestTheChildsExitCodeIsPropagated(t *testing.T) {
	var addr string
	code, err := Run(Config{LensURL: lensStub(t), LensAPIKey: "k", Log: io.Discard},
		[]string{"sh", "-c", "exit 7"},
		Stdio{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard},
		func(s *Sidecar) { addr = strings.TrimPrefix(s.BaseURL(), "http://") })
	if err != nil {
		t.Fatalf("Run returned an error for a non-zero child: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if !portClosesWithin(5*time.Second, addr) {
		t.Errorf("the proxy outlived a failing child on %s", addr)
	}
}

// A crash is not a clean exit and must not be treated as one.
func TestTheProxyDiesWhenTheChildCrashes(t *testing.T) {
	var addr string
	code, err := Run(Config{LensURL: lensStub(t), LensAPIKey: "k", Log: io.Discard},
		[]string{"sh", "-c", "kill -9 $$"},
		Stdio{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard},
		func(s *Sidecar) { addr = strings.TrimPrefix(s.BaseURL(), "http://") })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code == 0 {
		t.Errorf("a SIGKILLed child reported success (exit %d)", code)
	}
	if !portClosesWithin(5*time.Second, addr) {
		t.Errorf("the proxy outlived a crashed child on %s", addr)
	}
}

// ⚠ THE SIGNAL PATH, TESTED FOR REAL.
//
// This does not simulate Ctrl-C by calling a handler — it runs a genuine process tree and
// interrupts it the way a terminal does. Reasoning about signal handling instead of exercising it
// is how orphaned processes happen.
//
// ⚠ IT SIGNALS THE PROCESS GROUP, NOT THE PROCESS, because that is what Ctrl-C actually is: the
// tty sends SIGINT to every process in the foreground group. Signalling only the parent tests a
// situation no terminal produces. The subprocess is therefore given its own group so the test can
// address the whole tree.
//
// ⚠ WHAT THIS TEST DOES AND DOES NOT PROVE, because the difference matters. It proves the TREE
// TERMINATES on Ctrl-C and releases the port. It canNOT detect a missing Close(), because the
// process exits either way and the kernel reclaims the socket — removing the defer from Run leaves
// this test green. The three tests above are what catch that: they stay in-process after Run
// returns, and all three fail when the defer is removed. Both halves are needed, and neither
// substitutes for the other.
func TestCtrlCKillsBothTheChildAndTheProxy(t *testing.T) {
	if os.Getenv("SIDECAR_SIGNAL_CHILD") == "1" {
		// Re-executed as the process under test: this is `talyvor-code exec -- sleep 30` in
		// miniature. It prints its port so the parent can dial it, then blocks in Run.
		code, err := Run(Config{LensURL: os.Getenv("SIDECAR_LENS"), LensAPIKey: "k", Log: io.Discard},
			[]string{"sh", "-c", os.Getenv("SIDECAR_CHILD_CMD")},
			// The child's own stdout reaches the parent, so it can announce readiness itself.
			Stdio{In: strings.NewReader(""), Out: os.Stdout, Err: io.Discard},
			func(s *Sidecar) {
				_, _ = fmt.Fprintf(os.Stdout, "PORT %s\n", strings.TrimPrefix(s.BaseURL(), "http://127.0.0.1:"))
				_ = os.Stdout.Sync()
			})
		if err != nil {
			os.Exit(90)
		}
		os.Exit(code)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate the test binary: %v", err)
	}
	cmd := exec.Command(exe, "-test.run", "^TestCtrlCKillsBothTheChildAndTheProxy$")
	childCmd := os.Getenv("SIDECAR_CHILD_CMD")
	if childCmd == "" {
		// ⚠ exec: see the note below. Each child command owns its own exec so the wrapper does not
		// prepend one to a compound command — prepending it turned the SIGINT-ignoring control into
		// `exec trap ""`, which is not a program, so the control exited instantly and passed.
		childCmd = "exec sleep 30"
	}
	// ⚠ THE CHILD ANNOUNCES ITSELF BEFORE IT IS SIGNALLED, and that is not ceremony. Ctrl-C
	// arrives here under a millisecond after launch, which is early enough that a shell has not
	// yet run its own first line — an earlier version of this test signalled a child before its
	// `trap` was installed, so the control child died like an ordinary one and the control passed
	// while proving nothing. A human pressing Ctrl-C is never that fast; waiting for the child to
	// say it is up removes the race instead of papering over it with a sleep.
	// ⚠ THE CHILD COMMANDS exec, SO THE CHILD IS ONE PROCESS AND NOT A SHELL WAITING ON A GRANDCHILD.
	//
	// This is what made the test fail on CI and pass everywhere else. `echo READY; sleep 30` makes
	// dash fork a grandchild AFTER announcing readiness, so a Ctrl-C landing in that window is
	// delivered to the group before `sleep` exists. The new `sleep` never sees it, and dash — which
	// did — defers its own death until its foreground child finishes. Measured directly: a shell
	// signalled that way took 29.7s to exit a 30s sleep, reporting "signal: interrupt" at the end.
	// The window is microseconds wide on an idle machine and wide enough on a loaded CI runner.
	//
	// exec removes the grandchild: the signal either arrives before it and kills the shell, or after
	// it and kills the sleep. Both are prompt, so readiness now means what the test reads it to
	// mean. SIG_IGN survives exec, so a SIGINT-ignoring control child still ignores it.
	childCmd = "echo " + childReady + "; " + childCmd
	cmd.Env = append(os.Environ(), "SIDECAR_SIGNAL_CHILD=1", "SIDECAR_LENS="+lensStub(t), "SIDECAR_CHILD_CMD="+childCmd)
	// Its own process group, so the test can deliver SIGINT to the whole tree exactly as a tty
	// would to the foreground group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// ⚠ WAIT IS NEVER CALLED CONCURRENTLY WITH READING THE PIPE. Wait closes StdoutPipe when the
	// process exits, so racing them is documented as incorrect — and doing it here made an earlier
	// version of this test report a still-listening proxy on a sidecar that had shut down
	// correctly. A watchdog handles the hang case instead, so every read below is synchronous.
	watchdog := time.AfterFunc(20*time.Second, func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	defer watchdog.Stop()

	port, ready := "", false
	br := bufio.NewReader(out)
	for port == "" || !ready {
		l, rerr := br.ReadString('\n')
		trimmed := strings.TrimSpace(l)
		if after, ok := strings.CutPrefix(trimmed, "PORT "); ok {
			port = after
		}
		if trimmed == childReady {
			ready = true
		}
		if port != "" && ready {
			break
		}
		if rerr != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			t.Fatalf("the subprocess ended before both the port and the child were ready (last line %q, err %v)", l, rerr)
		}
	}
	addr := "127.0.0.1:" + port

	// ⚠ POSITIVE CONTROL, INSIDE THE TEST. Without this, a sidecar that never started would sail
	// through the assertion below — "the port is closed" is trivially true of a port never opened.
	if portIsClosed(addr) {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Fatalf("the proxy was not listening on %s before the signal, so the assertion below would prove nothing", addr)
	}

	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Fatalf("sending SIGINT to the process group: %v", err)
	}
	signalledAt := time.Now()
	waitErr := cmd.Wait()
	// ⚠ PROMPTLY, not eventually. The watchdog above SIGKILLs a stuck tree, and a SIGKILLed
	// process also releases its port — so without a deadline on the exit itself, a sidecar that
	// ignored Ctrl-C entirely would still be recorded as passing, just twenty seconds later.
	if elapsed := time.Since(signalledAt); elapsed > 15*time.Second {
		t.Errorf("the tree took %v to exit after Ctrl-C; it did not act on the signal", elapsed)
	}

	if !portClosesWithin(5*time.Second, addr) {
		t.Errorf("the proxy is STILL LISTENING on %s after Ctrl-C (child exit: %v) — an orphaned proxy holding a Lens key", addr, waitErr)
	}
}

// An empty command is a usage error, not a silently-started proxy with nothing to protect.
func TestNoCommandIsRefused(t *testing.T) {
	_, err := Run(Config{LensURL: lensStub(t), LensAPIKey: "k", Log: io.Discard}, nil,
		Stdio{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}, nil)
	if err == nil {
		t.Fatal("an empty command was accepted")
	}
}

// ⚠ WITHOUT A LENS KEY THE TOOL MUST REFUSE, LOUDLY. Starting a proxy that cannot authenticate
// would send every request into a 401 and look like the model was broken.
func TestAMissingLensKeyIsRefusedBeforeAnythingStarts(t *testing.T) {
	_, err := Run(Config{LensURL: lensStub(t), LensAPIKey: "", Log: io.Discard},
		[]string{"sh", "-c", "exit 0"},
		Stdio{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}, nil)
	if err == nil {
		t.Fatal("the sidecar started without a Lens API key")
	}
	if !strings.Contains(err.Error(), "TALYVOR_LENS_API_KEY") {
		t.Errorf("the error does not name the variable to set: %v", err)
	}
}
