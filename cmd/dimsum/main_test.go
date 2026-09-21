package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLIValidationAndUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: ["), 0600))
	for _, args := range [][]string{{"validate", "-config", path}, {"serve", "-config", path}, {"unknown"}, {"validate", "extra"}} {
		var out, stderr bytes.Buffer
		assert.NotZero(t, run(context.Background(), args, &out, &stderr), "accepted %v", args)
		assert.NotEmpty(t, stderr.String(), "missing diagnostic")
	}
	var out, stderr bytes.Buffer
	assert.Zero(t, run(context.Background(), []string{"--help"}, &out, &stderr))
	assert.Contains(t, out.String(), "validate", "help not discoverable")
	assert.Zero(t, run(context.Background(), []string{"validate", "-config", "../../testdata/config/dimsum.yaml"}, &out, &stderr), stderr.String())
	assert.Contains(t, out.String(), `"active_available":false`, "offline validation pretends to know active state")
}

func TestForwardCLIServe(t *testing.T) {
	u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		p := append([]byte(nil), r.Wire...)
		p[2] |= 0x80
		return testutil.Response{Wire: p}
	})
	require.NoError(t, e)
	defer u.Close()
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	text := fmt.Sprintf("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [%s]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: ./data\n  secrets_dir: ./secrets\n", u.Address())
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		code := run(ctx, []string{"serve", "-config", path}, writer, &stderr)
		writer.Close()
		done <- code
	}()
	var status struct {
		State string
		Ready bool
		DNS   []string
	}
	require.NoError(t, json.NewDecoder(reader).Decode(&status))
	assert.Equal(t, "dns_serving", status.State)
	assert.True(t, status.Ready)
	require.Len(t, status.DNS, 1)
	conn, e := net.Dial("udp", status.DNS[0])
	require.NoError(t, e)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(time.Second)))
	_, e = conn.Write([]byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1})
	require.NoError(t, e)
	p := make([]byte, 512)
	n, e := conn.Read(p)
	require.NoError(t, e)
	assert.Greater(t, n, 12)
	assert.Equal(t, byte(0), p[3]&15)
	cancel()
	select {
	case code := <-done:
		assert.Zero(t, code, stderr.String())
	case <-time.After(time.Second):
		require.FailNow(t, "CLI shutdown stalled")
	}
}
