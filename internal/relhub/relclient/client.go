// Package relclient is the Go SDK of the release-publish server: check for
// updates, wait for new releases, download packages with resume and
// verification, and (with a token) publish and manage releases. It uses only
// the standard library and works on every platform Go targets.
//
//	c, _ := relclient.New("https://host/release", relclient.WithTrustedKeys(keys...))
//	u, err := c.Check(ctx, relclient.Target{App: "nightdesk", Platform: "android"}, currentVersionCode)
//	if err == nil && u.UpdateAvailable {
//		err = c.Download(ctx, *u.Manifest, "/tmp/update.apk", nil)
//	}
package relclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"go-nx/internal/relhub/relproto"
)

// Client talks to one release-publish server. It is safe for concurrent use.
type Client struct {
	base  string
	http  *http.Client
	token string
	keys  []ed25519.PublicKey
	agent string
	rpcID atomic.Int64

	pauseMin, pauseMax time.Duration // WatchLoop's pause after a network error
}

// Option configures a Client.
type Option func(*Client)

// WithToken sets the bearer token for calls that change things.
func WithToken(token string) Option { return func(c *Client) { c.token = token } }

// WithHTTPClient replaces the HTTP client (timeouts, proxies, TLS roots).
// Do not set Client.Timeout: downloads and long polls outlive any fixed value;
// use the contexts instead.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithTrustedKeys pins the signing keys. Every manifest the client hands out
// is then verified against them; without keys only the manifest's shape and
// the package checksum are checked, which protects against corruption but not
// against a malicious server.
func WithTrustedKeys(keys ...ed25519.PublicKey) Option {
	return func(c *Client) { c.keys = append(c.keys, keys...) }
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.agent = ua } }

// WithRetryPause sets how long WatchLoop waits after a network error: first
// `initial`, doubling up to `max`. The defaults are 1 s and 1 minute.
func WithRetryPause(initial, max time.Duration) Option {
	return func(c *Client) { c.pauseMin, c.pauseMax = initial, max }
}

// ParseTrustedKeys decodes base64 Ed25519 public keys (as shown by `relhub keys`).
func ParseTrustedKeys(b64 ...string) ([]ed25519.PublicKey, error) {
	out := make([]ed25519.PublicKey, 0, len(b64))
	for _, s := range b64 {
		k, err := relproto.ParsePublicKey(s)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// New creates a client for the server at baseURL, including any path prefix
// the reverse proxy adds (https://host/release).
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("relclient: %q is not an http(s) URL", baseURL)
	}
	c := &Client{base: strings.TrimRight(u.String(), "/"), http: &http.Client{}, agent: "relclient/1", pauseMin: time.Second, pauseMax: time.Minute}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// BaseURL returns the server URL the client was created with.
func (c *Client) BaseURL() string { return c.base }

// Target names what to update: an app on a platform and a channel (default stable).
type Target struct {
	App      string
	Platform string
	Channel  string
}

func (t Target) channel() string {
	if t.Channel == "" {
		return relproto.DefaultChannel
	}
	return t.Channel
}

// --- plumbing ---

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Request, error) {
	u := c.base + "/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.agent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

// apiError turns a non-2xx response into a *relproto.Error.
func apiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var eb relproto.ErrorBody
	if json.Unmarshal(raw, &eb) == nil && eb.Error != nil && eb.Error.Code != "" {
		return eb.Error
	}
	msg := strings.TrimSpace(string(raw))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	if msg == "" {
		msg = resp.Status
	}
	return &relproto.Error{Code: relproto.CodeFromStatus(resp.StatusCode), Message: fmt.Sprintf("%s: %s", resp.Status, msg)}
}

// doJSON sends in as JSON (if non-nil) and decodes a JSON answer into out (if non-nil).
func (c *Client) doJSON(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := c.newRequest(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return apiError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return decodeBody(resp.Body, out)
}

func decodeBody(r io.Reader, v any) error {
	if err := json.NewDecoder(io.LimitReader(r, 32<<20)).Decode(v); err != nil {
		return fmt.Errorf("relclient: unreadable answer: %w", err)
	}
	return nil
}

// verify checks a manifest's shape and, when keys are pinned, its signature.
func (c *Client) verify(m *relproto.Manifest) error {
	if m == nil {
		return relproto.Errorf(relproto.CodeInvalid, "the server sent no manifest")
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if len(c.keys) > 0 {
		return m.Verify(c.keys)
	}
	return nil
}

// Call invokes a JSON-RPC method (see the rpc.discover method for the list)
// and decodes the result into result (if non-nil). Failures come back as
// *relproto.Error.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	req := map[string]any{"jsonrpc": "2.0", "id": c.rpcID.Add(1), "method": method}
	if params != nil {
		req["params"] = params
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int               `json:"code"`
			Message string            `json:"message"`
			Data    map[string]string `json:"data"`
		} `json:"error"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "v1/rpc", nil, req, &reply); err != nil {
		return err
	}
	if reply.Error != nil {
		code := reply.Error.Data["code"]
		if code == "" {
			code = relproto.CodeInternal
			if reply.Error.Code == -32602 || reply.Error.Code == -32600 || reply.Error.Code == -32601 || reply.Error.Code == -32700 {
				code = relproto.CodeInvalid
			}
		}
		return &relproto.Error{Code: code, Message: reply.Error.Message}
	}
	if result == nil || len(reply.Result) == 0 {
		return nil
	}
	return json.Unmarshal(reply.Result, result)
}
