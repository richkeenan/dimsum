//go:build linux || darwin

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
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
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, mode := range []string{"sigint", "sigterm", "ctrl-c", "enter"} {
		t.Run(mode, func(t *testing.T) {
			master, slave, err := pty.Open()
			require.NoError(t, err)
			defer master.Close()
			defer slave.Close()
			state := func() unix.Termios {
				value, err := unix.IoctlGetTermios(int(slave.Fd()), loginGetTermios)
				require.NoError(t, err)
				// Darwin sets this transient retype-pending flag on a mode change.
				value.Lflag &^= unix.PENDIN
				return *value
			}
			before := state()
			dir := t.TempDir()
			cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestLoginPromptProcess$")
			cmd.Env = append(os.Environ(), "DIMSUM_TEST_LOGIN_PROMPT=1", "XDG_CONFIG_HOME="+dir)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			require.NoError(t, cmd.Start())
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			waited := false
			defer func() {
				if !waited {
					_ = cmd.Process.Kill()
					<-done
				}
			}()

			var output strings.Builder
			readOutput := func() {
				for {
					fds := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
					n, err := unix.Poll(fds, 0)
					if err == unix.EINTR {
						continue
					}
					require.NoError(t, err)
					if n == 0 {
						return
					}
					var buffer [4096]byte
					n, err = master.Read(buffer[:])
					require.NoError(t, err)
					output.Write(buffer[:n])
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				readOutput()
				if strings.Contains(output.String(), "Dashboard password:") && state().Lflag&unix.ECHO == 0 {
					break
				}
				require.True(t, time.Now().Before(deadline), "hidden prompt did not appear: %s", output.String())
				time.Sleep(20 * time.Millisecond)
			}
			switch mode {
			case "enter":
				_, err = master.WriteString("fixture-secret\r")
			case "ctrl-c":
				_, err = master.WriteString("\x03")
			case "sigint":
				err = cmd.Process.Signal(syscall.SIGINT)
			case "sigterm":
				err = cmd.Process.Signal(syscall.SIGTERM)
			}
			require.NoError(t, err)
			select {
			case err := <-done:
				waited = true
				assert.Error(t, err, "cancelled or rejected login reported success")
			case <-time.After(2 * time.Second):
				t.Fatal("login remained blocked after cancellation without Enter")
			}
			assert.Equal(t, before, state(), "terminal settings were not restored")
			readOutput()
			if mode == "enter" {
				data, err := os.ReadFile(filepath.Join(dir, "submitted.json"))
				require.NoError(t, err)
				assert.JSONEq(t, `{"password":"fixture-secret"}`, string(data))
				assert.NotContains(t, output.String(), "fixture-secret")
				assert.Contains(t, output.String(), "Login rejected")
			} else {
				assert.Contains(t, output.String(), "Login cancelled.")
				assert.NotContains(t, output.String(), "context canceled")
				assert.NotContains(t, output.String(), "EOF")
			}
		})
	}
}
