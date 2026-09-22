package cli

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// readPassword owns terminal mode for the duration of the prompt. Polling the
// input lets signal cancellation return through Restore without a blocked reader
// goroutine retaining control of stdin.
func readPassword(ctx context.Context, input *os.File, output io.Writer) (password []byte, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	fd := int(input.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, term.Restore(fd, state)) }()
	terminal := term.NewTerminal(&passwordTerminal{ctx: ctx, fd: fd, Writer: output}, "")
	line, err := terminal.ReadPassword("Password: ")
	return []byte(line), err
}

type passwordTerminal struct {
	ctx context.Context
	fd  int
	io.Writer
}

func (r *passwordTerminal) Read(buf []byte) (int, error) {
	fds := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		n, err := unix.Poll(fds, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			continue
		}
		if fds[0].Revents&unix.POLLNVAL != 0 {
			return 0, unix.EBADF
		}
		n, err = unix.Read(r.fd, buf)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		return n, nil
	}
}
