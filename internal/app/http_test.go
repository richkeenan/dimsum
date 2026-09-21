package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminHTTPStopsActiveConnections(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	entered := make(chan struct{})
	go func() {
		done <- serveHTTP(ctx, l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	}()
	clientDone := make(chan error, 1)
	go func() {
		c := http.Client{Timeout: 3 * time.Second}
		r, err := c.Get("http://" + l.Addr().String())
		if err == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}
		clientDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler not entered")
	}
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP shutdown leaked")
	}
	select {
	case <-clientDone:
	case <-time.After(time.Second):
		t.Fatal("client connection not closed")
	}
}

func TestLimitedListenerShutdownUnblocksAdmission(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	limited := &limitedListener{Listener: l, slots: make(chan struct{}, 1), closed: make(chan struct{})}
	defer limited.Close()
	c, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	defer c.Close()
	accepted, err := limited.Accept()
	require.NoError(t, err)
	defer accepted.Close()
	done := make(chan error, 1)
	go func() { _, err := limited.Accept(); done <- err }()
	require.NoError(t, limited.Close())
	select {
	case err := <-done:
		assert.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("admission wait not closed")
	}
}
