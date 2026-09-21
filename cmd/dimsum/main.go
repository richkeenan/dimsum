package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
)

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	const usage = "Usage: dimsum <serve|validate> -config path/to/dimsum.yaml\n\nserve opens lifecycle skeleton listeners (DNS handlers not yet implemented).\nvalidate checks configuration offline and prints JSON.\nExit codes: 0 success, 1 validation/runtime error, 2 usage error.\n"
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, usage)
		return 0
	}
	if args[0] != "serve" && args[0] != "validate" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "dimsum.yaml", "authoritative configuration file")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	b, err := os.ReadFile(*path)
	if err != nil {
		return fail(err)
	}
	d, err := config.Parse(b)
	if err != nil {
		return fail(err)
	}
	encoder := json.NewEncoder(out)
	if args[0] == "validate" {
		if err := encoder.Encode(struct {
			Valid           bool   `json:"valid"`
			Revision        string `json:"revision"`
			ActiveAvailable bool   `json:"active_available"`
		}{true, d.Revision(), false}); err != nil {
			return fail(err)
		}
		return 0
	}
	s := new(app.Service)
	if err := s.Start(ctx, d.Config()); err != nil {
		return fail(err)
	}
	defer s.Close()
	a := s.Addresses()
	if err := encoder.Encode(struct {
		State string   `json:"state"`
		Ready bool     `json:"ready"`
		DNS   []string `json:"dns"`
		Admin string   `json:"admin"`
	}{"listeners_open", false, a.DNS, a.Admin}); err != nil {
		return fail(err)
	}
	<-s.Done()
	return 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
