package cli

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type failedAuthBody struct{ err error }

func (b failedAuthBody) Read([]byte) (int, error) { return 0, b.err }

type authResponseTransport struct{ body io.Reader }

func (t authResponseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(t.body), Header: make(http.Header)}, nil
}

func TestAuthResponseBodyFailureClassification(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
		t.Run(cause.Error(), func(t *testing.T) {
			client := &http.Client{Transport: authResponseTransport{io.MultiReader(strings.NewReader("{"), failedAuthBody{cause})}}
			var response map[string]any
			err := authRequest(t.Context(), client, credentials{Server: "http://dns.example"}, "POST", "/session", nil, "", &response)
			want := cause
			if cause == io.ErrUnexpectedEOF {
				want = errUnexpectedResponse
			}
			assert.ErrorIs(t, err, want)
		})
	}
}
