package transport

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"
)

type datagram struct {
	slot *RequestSlot
	peer netip.AddrPort
}

// ServeUDP runs fixed workers until cancellation or socket failure, and waits
// for all borrowed storage to be released. Cancellation closes the socket.
// Call once per socket. There is no goroutine creation per packet.
func (s *Server) ServeUDP(ctx context.Context, c *net.UDPConn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	jobs := make(chan datagram, s.opts.SmallSlots+s.opts.LargeSlots)
	var workers sync.WaitGroup
	// Serialize writes and their shared socket deadline. Workers never block past
	// WriteTimeout; no worker can change a different writer's deadline.
	var writeMu sync.Mutex
	for range s.opts.Workers {
		workers.Go(func() {
			out := make([]byte, 65535)
			for job := range jobs {
				if ctx.Err() == nil {
					n := s.resolve(ctx, job.slot.wire, out, job.peer, false, job.slot.deadline)
					if n > 0 && ctx.Err() == nil {
						writeMu.Lock()
						c.SetWriteDeadline(time.Now().Add(s.opts.WriteTimeout))
						_, err := c.WriteToUDPAddrPort(out[:n], job.peer)
						writeMu.Unlock()
						if err != nil {
							s.stats.writeErrors.Add(1)
						}
					}
				}
				s.pool.release(job.slot)
			}
		})
	}
	scratch := make([]byte, 65535)
	var result error
	for {
		n, peer, err := c.ReadFromUDPAddrPort(scratch)
		if err != nil {
			if ctx.Err() == nil && !isClosed(err) {
				result = err
			}
			break
		}
		slot := s.acquire(n)
		if slot == nil {
			continue
		}
		slot.wire = slot.wire[:n]
		copy(slot.wire, scratch[:n])
		select {
		case jobs <- datagram{slot, peer}:
		case <-ctx.Done():
			s.pool.release(slot)
		}
		if ctx.Err() != nil {
			break
		}
	}
	cancel()
	close(jobs)
	workers.Wait()
	return result
}

func isClosed(err error) bool { return errors.Is(err, net.ErrClosed) }
