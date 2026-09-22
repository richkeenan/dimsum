package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoginPromptProcess(t *testing.T) {
	if os.Getenv("DIMSUM_TEST_LOGIN_PROMPT") != "1" {
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err == nil {
			err = os.WriteFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "submitted.json"), data, 0600)
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	os.Args = []string{"dimsum", "login", "--server", server.URL}
	main()
}

func TestLoginPromptCancellationRestoresTerminal(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for the pseudo-terminal integration test")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, mode := range []string{"sigint", "sigterm", "ctrl-c", "enter"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), python, "-c", loginPromptPTY, binary, t.TempDir(), mode)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}

const loginPromptPTY = `
import json, os, pty, select, signal, subprocess, sys, termios, time
master, slave = pty.openpty()
before = termios.tcgetattr(slave)
env = dict(os.environ, DIMSUM_TEST_LOGIN_PROMPT="1", XDG_CONFIG_HOME=sys.argv[2])
child = subprocess.Popen([sys.argv[1], "-test.run=^TestLoginPromptProcess$"],
                         stdin=slave, stdout=slave, stderr=slave, env=env,
                         start_new_session=True)
try:
    output = b""
    deadline = time.monotonic() + 5
    while b"Password:" not in output or termios.tcgetattr(slave)[3] & termios.ECHO:
        assert time.monotonic() < deadline, "hidden prompt did not appear: %r" % output
        if select.select([master], [], [], 0.02)[0]:
            output += os.read(master, 4096)
    if sys.argv[3] == "enter":
        os.write(master, b"fixture-secret\r")
    elif sys.argv[3] == "ctrl-c":
        os.write(master, b"\x03")
    else:
        child.send_signal(signal.SIGINT if sys.argv[3] == "sigint" else signal.SIGTERM)
    try:
        code = child.wait(timeout=2)
    except subprocess.TimeoutExpired:
        raise AssertionError("login remained blocked after cancellation without Enter")
    assert code != 0, "cancelled login reported success"
    after = termios.tcgetattr(slave)
    # Darwin sets this transient retype-pending flag on a mode change.
    before[3] &= ~getattr(termios, "PENDIN", 0)
    after[3] &= ~getattr(termios, "PENDIN", 0)
    assert after == before, "terminal settings were not restored: before=%r after=%r" % (before, after)
    if sys.argv[3] == "enter":
        with open(os.path.join(sys.argv[2], "submitted.json")) as f:
            assert json.load(f) == {"password": "fixture-secret"}
        while select.select([master], [], [], 0)[0]:
            output += os.read(master, 4096)
        assert b"fixture-secret" not in output, "password appeared in terminal output"
finally:
    if child.poll() is None:
        child.kill()
        child.wait()
    termios.tcsetattr(slave, termios.TCSANOW, before)
    os.close(master)
    os.close(slave)
`
