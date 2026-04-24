// Package adminclient is a tiny HTTP client that talks to the daemon over a
// UNIX socket (typically ~/.mcp-gateway/sock).
package adminclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client makes HTTP requests over a UNIX socket.
type Client struct {
	http *http.Client
	sock string
}

// New returns a Client that dials the given UNIX socket path.
func New(sock string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}
	return &Client{
		http: &http.Client{Transport: tr, Timeout: 10 * time.Second},
		sock: sock,
	}
}

// Get GETs path and decodes JSON into into (if non-nil).
func (c *Client) Get(path string, into any) error {
	return c.do(http.MethodGet, path, nil, into)
}

// Post POSTs body (json-marshaled if non-nil) to path and decodes JSON into into.
func (c *Client) Post(path string, body, into any) error {
	return c.do(http.MethodPost, path, body, into)
}

// Delete sends a DELETE.
func (c *Client) Delete(path string) error {
	return c.do(http.MethodDelete, path, nil, nil)
}

func (c *Client) do(method, path string, body, into any) error {
	url := "http://unix" + path
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("adminclient: marshal: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return fmt.Errorf("adminclient: new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("adminclient: do (sock=%s): %w", c.sock, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("adminclient: %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if into == nil {
		return nil
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("adminclient: decode: %w", err)
	}
	return nil
}
