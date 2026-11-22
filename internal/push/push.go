// Package push sends a status payload to the push service client API.
package push

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// timeout bounds a single push attempt.
const timeout = 30 * time.Second

// errBodyLimit caps how much of an error response body is kept for logging.
const errBodyLimit = 256

// Sender POSTs status payloads to the push service.
type Sender struct {
	client *http.Client
}

// New builds a Sender.
func New() *Sender {
	return &Sender{client: &http.Client{Timeout: timeout}}
}

// Send POSTs payload to url/name with the raw client token. url must be an
// endpoint without trailing slash (the config normalizes it). Any 2xx
// response counts as success; the service answers 200 for every well-formed
// request, so a non-2xx means the network path or the service is broken.
func (s *Sender) Send(ctx context.Context, url, name, token string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/"+name, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-Client-Token", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	// The body is fully read (or drained) above, so a close error can only
	// signal a transport anomaly: the status is what matters.
	defer resp.Body.Close() //nolint:errcheck // see above

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
		if rerr != nil {
			return fmt.Errorf("service answered %s", resp.Status)
		}
		return fmt.Errorf("service answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	// Drain the (small) success body so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) //nolint:errcheck // best-effort drain; the deferred Close is what matters
	return nil
}
