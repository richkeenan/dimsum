package cli_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenAliases(t *testing.T) {
	for _, tc := range []struct {
		args               []string
		method, path, body string
	}{
		{[]string{"tokens"}, "GET", "/api/v1/tokens", ""},
		{[]string{"token-create", `{"name":"agent"}`}, "POST", "/api/v1/tokens", `{"name":"agent"}`},
		{[]string{"token-revoke", "abc"}, "DELETE", "/api/v1/tokens/abc", ""},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "c.sock")
			l, err := net.Listen("unix", socket)
			require.NoError(t, err)
			type call struct {
				method, path, body string
				err                error
			}
			calls := make(chan call, 1)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				calls <- call{r.Method, r.URL.Path, string(body), err}
				_, _ = io.WriteString(w, `{"ok":true}`)
			})}
			done := make(chan error, 1)
			go func() { done <- server.Serve(l) }()
			t.Cleanup(func() { require.NoError(t, server.Close()); require.ErrorIs(t, <-done, http.ErrServerClosed) })
			var out, stderr bytes.Buffer
			code := cli.Run(context.Background(), append([]string{"--socket", socket}, tc.args...), &out, &stderr)
			require.Equal(t, 0, code, stderr.String())
			got := <-calls
			require.NoError(t, got.err)
			assert.Equal(t, tc.method, got.method)
			assert.Equal(t, tc.path, got.path)
			assert.Equal(t, tc.body, got.body)
			assert.JSONEq(t, `{"ok":true}`, out.String())
		})
	}
}

func TestTokenAliasUsage(t *testing.T) {
	for _, args := range [][]string{{"tokens", "extra"}, {"token-create"}, {"token-revoke"}, {"token-revoke", "a", "b"}} {
		var out, stderr bytes.Buffer
		assert.Equal(t, 2, cli.Run(context.Background(), args, &out, &stderr))
	}
	assert.Contains(t, cli.Help, "tokens")
	assert.Contains(t, cli.Help, "token-create JSON")
	assert.Contains(t, cli.Help, "token-revoke ID")
}
