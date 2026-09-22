package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type credentials struct {
	Server string `json:"server"`
	ID     string `json:"id"`
	Token  string `json:"token"`
}

type statusError struct {
	method, path string
	code         int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d", e.method, e.path, e.code)
}

func credentialPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("user configuration directory must be absolute")
	}
	return filepath.Join(dir, "dimsum", "credentials.json"), nil
}

func serverURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("server must be an http:// or https:// origin, without a path, credentials, query or fragment")
	}
	return u.Scheme + "://" + u.Host, nil
}

func readCredentials() (credentials, error) {
	var c credentials
	path, err := credentialPath()
	if err != nil {
		return c, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return c, errors.New("CLI credentials must be an owner-only regular file (0600)")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	if !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return c, errors.New("CLI credential directory must be owner-only (0700)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, errors.New("invalid CLI credentials; remove the credentials file and run dimsum login")
	}
	c.Server, err = serverURL(c.Server)
	if err != nil || c.ID == "" || c.Token == "" {
		return c, errors.New("invalid saved CLI connection")
	}
	return c, nil
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func authRequest(ctx context.Context, client *http.Client, c credentials, method, path string, body any, csrf string, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Server+path, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if csrf != "" {
		req.Header.Set("Origin", c.Server)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{method, path, resp.StatusCode}
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
			var syntax *json.SyntaxError
			var shape *json.UnmarshalTypeError
			if errors.As(err, &syntax) || errors.As(err, &shape) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return errUnexpectedResponse
			}
			return err
		}
		return nil
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return err
}

func login(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(stderr)
	server := flags.String("server", "http://127.0.0.1:8080", "dashboard origin")
	passwordFile := flags.String("password-file", "", "read password from file, or - for stdin")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: dimsum login [--server URL] [--password-file PATH|-]\n\nConnect to a running dimsum server using its dashboard password.\nA fresh server uses admin; rebuilding does not reset an existing password.\nLogin saves a private token so control commands can run without sudo.\n\nExamples:\n  dimsum login\n  dimsum login --server https://dns.example.net\n\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: dimsum login [--server URL] [--password-file FILE|-]")
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 3 }
	base, err := serverURL(*server)
	if err != nil {
		return fail(err)
	}
	path, err := credentialPath()
	if err != nil {
		return fail(err)
	}
	if _, err = os.Lstat(path); err == nil {
		return fail(errors.New("already logged in; run dimsum logout before changing connections"))
	} else if !os.IsNotExist(err) {
		return fail(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fail(err)
	}
	info, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return fail(err)
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fail(errors.New("CLI credential directory must be owner-only (0700)"))
	}
	fmt.Fprintln(stderr, "Logging in to "+base)
	var password []byte
	if *passwordFile == "" {
		password, err = readPassword(ctx, os.Stdin, stderr)
		if err != nil {
			fmt.Fprintln(stderr)
		}
	} else {
		var reader io.Reader = os.Stdin
		if *passwordFile != "-" {
			f, e := os.Open(*passwordFile)
			if e != nil {
				return fail(e)
			}
			defer f.Close()
			reader = f
		}
		password, err = io.ReadAll(io.LimitReader(reader, 4097))
		password = []byte(strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r"))
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || (*passwordFile == "" && errors.Is(err, io.EOF)) {
			return fail(errors.New("Login cancelled."))
		}
		return fail(err)
	}
	if len(password) == 0 || len(password) > 4096 {
		return fail(errors.New("password must contain 1 to 4096 bytes"))
	}
	client := httpClient()
	client.Jar, _ = cookiejar.New(nil)
	c := credentials{Server: base}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err = authRequest(ctx, client, c, "POST", "/session", map[string]string{"password": string(password)}, "", &session); err != nil {
		return fail(errors.New(authMessage("Login", base, err)))
	}
	// The temporary browser session is used only to mint the CLI token.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = authRequest(cleanup, client, credentials{Server: base}, "DELETE", "/session", nil, session.CSRF, nil)
	}()
	if session.CSRF == "" {
		return fail(errors.New(authMessage("Login", base, errUnexpectedResponse)))
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err = authRequest(ctx, client, c, "POST", "/api/v1/tokens", map[string]string{"name": "dimsum CLI"}, session.CSRF, &created); err != nil {
		return fail(errors.New(authMessage("Saving CLI access", base, err)))
	}
	c.ID, c.Token = created.ID, created.Token
	if c.ID == "" || c.Token == "" {
		return fail(errors.New(authMessage("Saving CLI access", base, errUnexpectedResponse)))
	}
	err = saveCredentials(path, c)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if revokeErr := authRequest(cleanup, client, c, "DELETE", "/api/v1/tokens/"+url.PathEscape(c.ID), nil, "", nil); revokeErr != nil {
			fmt.Fprintln(stderr, "could not revoke unsaved CLI token:", revokeErr)
		}
		return fail(fmt.Errorf("could not save CLI login to %s: %w", path, err))
	}
	fmt.Fprintln(out, "Logged in to "+base+".\nYou can now run dimsum control settings without sudo.\nUse dimsum control help for commands, or dimsum logout to sign out.")
	return 0
}

func saveCredentials(path string, c credentials) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = json.NewEncoder(f).Encode(c); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Link publishes the complete file without overwriting a concurrent login.
	return os.Link(f.Name(), path)
}

func logout(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprintln(out, "Usage: dimsum logout\n\nRevoke this user's CLI token and remove the saved connection.\nIf the server is unreachable, keep the connection so you can retry.")
		return 0
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: dimsum logout")
		return 2
	}
	c, err := readCredentials()
	if os.IsNotExist(err) {
		fmt.Fprintln(out, "Already logged out.")
		return 0
	}
	if err == nil {
		fmt.Fprintln(stderr, "Logging out of "+c.Server)
		err = authRequest(ctx, httpClient(), c, "DELETE", "/api/v1/tokens/"+url.PathEscape(c.ID), nil, "", nil)
		var status *statusError
		if errors.As(err, &status) && (status.code == http.StatusUnauthorized || status.code == http.StatusNotFound) {
			err = nil
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, authMessage("Logout", c.Server, err))
		if c.Server != "" {
			fmt.Fprintln(stderr, "Your saved connection has been kept. Try dimsum logout again when the server is reachable.")
		}
		return 3
	}
	path, err := credentialPath()
	if err == nil {
		err = os.Remove(path)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	fmt.Fprintln(out, "Logged out; CLI token revoked.")
	return 0
}
