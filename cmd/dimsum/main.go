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
	"runtime"
	"syscall"

	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/cli"
	"github.com/richkeenan/dimsum/internal/config"
)

var version = "dev"
var commit = "unknown"
var date = "unknown"

func run(ctx context.Context, args []string, out, stderr io.Writer) (code int) {
	if len(args) > 0 && (args[0] == "control" || args[0] == "login" || args[0] == "logout") {
		return cli.Run(ctx, args, out, stderr)
	}
	if len(args) > 0 && args[0] == "bootstrap" {
		return bootstrap(args[1:], out, stderr)
	}
	if len(args) == 1 && args[0] == "version" {
		if err := json.NewEncoder(out).Encode(map[string]string{"version": version, "commit": commit, "build_time": date, "go_version": runtime.Version()}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	const usage = "Usage: dimsum <serve|validate> -config path/to/dimsum.yaml [-state path/to/derived-state]\n       dimsum login [--server URL] [--password-file PATH|-]\n       dimsum logout\n       dimsum control [--socket PATH] help\n       dimsum bootstrap -config PATH <-password-file PATH|-generate>\n       dimsum version\n\nserve forwards DNS and serves administration; watches configuration edits.\n-state defaults to CONFIG.state; retain this directory for offline recovery.\nvalidate checks configuration offline and prints JSON.\nExit codes: 0 success, 1 validation/runtime error, 2 usage error.\n"
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
	state := flags.String("state", "", "derived list/recovery directory (default CONFIG.state)")
	var cpuProfile, heapProfile string
	if args[0] == "serve" {
		flags.StringVar(&cpuProfile, "cpu-profile", "", "write CPU profile to a new local file until shutdown")
		flags.StringVar(&heapProfile, "heap-profile", "", "write heap/allocation profile to a new local file on shutdown")
	}
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
	encoder := json.NewEncoder(out)
	if args[0] == "validate" {
		b, err := os.ReadFile(*path)
		if err != nil {
			return fail(err)
		}
		d, err := config.Parse(b)
		if err != nil {
			return fail(err)
		}
		if err := encoder.Encode(struct {
			Valid           bool         `json:"valid"`
			Revision        string       `json:"revision"`
			ActiveAvailable bool         `json:"active_available"`
			Cache           config.Cache `json:"cache"`
		}{true, d.Revision(), false, d.Config().Cache}); err != nil {
			return fail(err)
		}
		return 0
	}
	if *state == "" {
		*state = *path + ".state"
	}
	store, err := config.OpenStore(ctx, *path, *state, config.StoreOptions{})
	if err != nil {
		return fail(err)
	}
	stopProfiles, err := startProfiles(cpuProfile, heapProfile)
	if err != nil {
		return fail(err)
	}
	defer func() {
		if err := stopProfiles(); err != nil {
			fmt.Fprintln(stderr, "writing profiles:", err)
			code = 1
		}
	}()
	s := new(app.Service)
	if err := s.StartManaged(ctx, store); err != nil {
		return fail(err)
	}
	defer s.Close()
	a := s.Addresses()
	if err := encoder.Encode(struct {
		State      string                  `json:"state"`
		Ready      bool                    `json:"ready"`
		DNS        []string                `json:"dns"`
		Admin      string                  `json:"admin"`
		Control    string                  `json:"control"`
		Activation config.ActivationResult `json:"activation"`
	}{"dns_serving", s.Ready(), a.DNS, a.Admin, a.Control, store.Inspect()}); err != nil {
		return fail(err)
	}
	<-s.Done()
	if err := s.Err(); err != nil {
		return fail(err)
	}
	return 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
