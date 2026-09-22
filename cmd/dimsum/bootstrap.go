package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/config"
)

func bootstrap(args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	f.SetOutput(stderr)
	path := f.String("config", "dimsum.yaml", "configuration path")
	password := f.String("password-file", "", "owner-only password file, or - for stdin")
	generate := f.Bool("generate", false, "generate a random password and print it once as JSON")
	ifNeeded := f.Bool("if-needed", false, "with -generate, preserve an existing credential (installer retries)")
	if f.Parse(args) != nil || f.NArg() != 0 || (*password == "") == !*generate || (*ifNeeded && !*generate) {
		fmt.Fprintln(stderr, "bootstrap requires -config PATH and exactly one of -password-file PATH (or -) or -generate")
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
	c := d.Config()
	dir := c.Paths.SecretsDir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(*path), dir)
	}
	if *ifNeeded {
		credential := filepath.Join(dir, config.AdminSecretName)
		if c.Admin.SecretGeneration != "" {
			credential = filepath.Join(dir, "generations", c.Admin.SecretGeneration, config.AdminSecretName)
		}
		if _, err := admin.LoadPasswordHash(credential); err == nil {
			if err := json.NewEncoder(out).Encode(map[string]bool{"created": false}); err != nil {
				return fail(err)
			}
			return 0
		} else if !os.IsNotExist(err) || c.Admin.SecretGeneration != "" {
			return fail(err)
		}
	}
	if c.Admin.SecretGeneration != "" {
		return fail(fmt.Errorf("bootstrap cannot replace an active credential generation"))
	}
	var reader io.Reader = os.Stdin
	if *generate {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return fail(err)
		}
		reader = strings.NewReader(base64.RawURLEncoding.EncodeToString(random[:]))
	} else if *password != "-" {
		info, err := os.Lstat(*password)
		if err != nil {
			return fail(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fail(fmt.Errorf("password file must be owner-only and regular"))
		}
		file, err := os.Open(*password)
		if err != nil {
			return fail(err)
		}
		defer file.Close()
		reader = file
	}
	b, err = io.ReadAll(io.LimitReader(reader, 1026))
	if err != nil {
		return fail(err)
	}
	secret := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return fail(err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fail(err)
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fail(fmt.Errorf("secrets directory must be owner-only"))
	}
	if err = admin.BootstrapPassword(filepath.Join(dir, config.AdminSecretName), secret); err != nil {
		return fail(err)
	}
	result := map[string]any{"created": true}
	if *generate {
		result["password"] = secret
	}
	if err = json.NewEncoder(out).Encode(result); err != nil {
		return fail(err)
	}
	return 0
}
