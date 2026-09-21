package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Administration has its own finite connection budget, independent of DNS.
// The context is also the base context for handlers (including SSE streams).
func serveHTTP(ctx context.Context, listener net.Listener, handler http.Handler) error {
	l := &limitedListener{Listener: listener, slots: make(chan struct{}, 64), closed: make(chan struct{})}
	s := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if s.Shutdown(shutdown) != nil {
				s.Close()
			}
		case <-done:
		}
	}()
	err := s.Serve(l)
	close(done)
	// Ensure active connections are closed even if Accept failed unexpectedly.
	s.Close()
	<-watchDone
	if ctx.Err() != nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

type limitedListener struct {
	net.Listener
	slots  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (l *limitedListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.closed:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitedConn{Conn: c, release: func() { <-l.slots }}, nil
}
func (l *limitedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return l.Listener.Close()
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
