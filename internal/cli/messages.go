package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

var errUnexpectedResponse = errors.New("unexpected server response")

// Authentication commands are interactive; control responses remain JSON for
// scripts. Do not display arbitrary response bodies or authentication material.
func authMessage(action, server string, err error) string {
	if errors.Is(err, context.Canceled) {
		return action + " cancelled."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("%s timed out connecting to %s. Check that the server is running and reachable.", action, server)
	}
	var status *statusError
	if errors.As(err, &status) {
		switch {
		case status.code == 401 && status.path == "/session":
			return fmt.Sprintf("Login rejected by %s.\nCheck that this is your dimsum dashboard address and use its password.\nThe default password is admin only when the server has no saved credential.\nTo connect elsewhere, run dimsum login --server URL.", server)
		case status.code == 401:
			return fmt.Sprintf("%s could not authenticate with %s. Run dimsum login again.", action, server)
		case status.code == 403:
			return fmt.Sprintf("%s is not allowed by %s. Check the server's allowed hosts and HTTPS settings.", action, server)
		case status.code == 404 || status.code == 405:
			return fmt.Sprintf("%s does not appear to provide the required dimsum API.\nCheck your dashboard address and server version. Use dimsum login --server URL to choose another address.", server)
		case status.code == 429 && status.path == "/session":
			return "Too many login attempts or active sessions. Wait a minute before trying again; if this persists, sign out of an unused dashboard session."
		case status.code == 429:
			return fmt.Sprintf("%s cannot create another CLI token right now. Remove an unused token in Settings → Agent access, then try again.", server)
		case status.code == 503:
			return fmt.Sprintf("%s is not ready for %s. Check the server's status and try again.", server, action)
		case status.code >= 300 && status.code < 400:
			return fmt.Sprintf("%s returned a redirect. Log in using the final dashboard address with dimsum login --server URL.", server)
		default:
			return fmt.Sprintf("%s failed: %s returned HTTP %d. Check the server logs for details.", action, server, status.code)
		}
	}
	if errors.Is(err, errUnexpectedResponse) {
		return fmt.Sprintf("Received an unexpected response from %s. Check that this is your dimsum dashboard address and that the server is up to date.", server)
	}
	var network *url.Error
	if errors.As(err, &network) {
		return fmt.Sprintf("Could not connect to %s.\nCheck that dimsum is running, or choose its dashboard address with dimsum login --server URL.\nConnection details: %v", server, network.Err)
	}
	return fmt.Sprintf("%s failed: %v", action, err)
}
