package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
)

// runtime/pprof does not return asynchronous CPU-profile writer errors. Record
// them here and read them only after StopCPUProfile has joined the writer.
type profileWriter struct {
	w   io.Writer
	err error
}

func (w *profileWriter) Write(b []byte) (int, error) {
	n, err := w.w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if w.err == nil {
		w.err = err
	}
	return n, err
}

func startCPUProfile(dst io.Writer) (func() error, error) {
	w := &profileWriter{w: dst}
	if err := pprof.StartCPUProfile(w); err != nil {
		return nil, err
	}
	return func() error {
		pprof.StopCPUProfile()
		return w.err
	}, nil
}

// Profiles are opt-in local files, never an HTTP listener. Open both destinations
// before starting collection so path errors cannot leave a half-started profiler.
func startProfiles(cpuPath, heapPath string) (func() error, error) {
	open := func(path string) (*os.File, error) {
		if path == "" {
			return nil, nil
		}
		return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	}
	cpu, err := open(cpuPath)
	if err != nil {
		return nil, fmt.Errorf("CPU profile: %w", err)
	}
	heap, err := open(heapPath)
	if err != nil {
		if cpu != nil {
			_ = cpu.Close()
		}
		return nil, fmt.Errorf("heap profile: %w", err)
	}
	var stopCPU func() error
	if cpu != nil {
		stopCPU, err = startCPUProfile(cpu)
		if err != nil {
			_ = cpu.Close()
			if heap != nil {
				_ = heap.Close()
			}
			return nil, fmt.Errorf("CPU profile: %w", err)
		}
	}
	return func() error {
		var errs []error
		if cpu != nil {
			errs = append(errs, stopCPU(), cpu.Close())
		}
		if heap != nil {
			runtime.GC()
			errs = append(errs, pprof.WriteHeapProfile(heap), heap.Close())
		}
		return errors.Join(errs...)
	}, nil
}
