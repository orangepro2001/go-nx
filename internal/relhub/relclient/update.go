package relclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/orangepro2001/go-nx/internal/relhub/relproto"
)

// ErrChecksum means a downloaded package does not match its manifest.
var ErrChecksum = errors.New("relclient: the downloaded package does not match its manifest")

// Info returns the server's name, API version and signing keys. The keys it
// lists are only as trustworthy as the connection: pin them with
// WithTrustedKeys instead of fetching them at run time.
func (c *Client) Info(ctx context.Context) (relproto.Info, error) {
	var info relproto.Info
	err := c.doJSON(ctx, http.MethodGet, "v1/info", nil, nil, &info)
	return info, err
}

// settle recomputes the verdict from the signed manifest: the booleans in the
// answer are not signed, so a client must not rely on them.
func (c *Client) settle(res *relproto.CheckResult, t Target, have int64) error {
	if err := c.verify(res.Manifest); err != nil {
		return err
	}
	m := res.Manifest
	if m.App != t.App || m.Platform != t.Platform || m.Channel != t.channel() {
		return relproto.Errorf(relproto.CodeInvalid, "the server answered with the manifest of %s/%s/%s", m.App, m.Platform, m.Channel)
	}
	res.CurrentVersionCode = have
	res.UpdateAvailable = m.VersionCode > have
	res.Mandatory = res.UpdateAvailable && m.Mandatory(have)
	res.DownloadPath = m.DownloadPath()
	return nil
}

func checkQuery(t Target, have int64) url.Values {
	q := url.Values{"platform": {t.Platform}, "channel": {t.channel()}, "versionCode": {strconv.FormatInt(have, 10)}}
	return q
}

// Check asks whether a release newer than versionCode `have` is offered. The
// result's Manifest is verified; UpdateAvailable and Mandatory are derived from
// it. A server that has nothing published answers not_found.
func (c *Client) Check(ctx context.Context, t Target, have int64) (*relproto.CheckResult, error) {
	var res relproto.CheckResult
	if err := c.doJSON(ctx, http.MethodGet, "v1/apps/"+url.PathEscape(t.App)+"/check", checkQuery(t, have), nil, &res); err != nil {
		return nil, err
	}
	if err := c.settle(&res, t, have); err != nil {
		return nil, err
	}
	return &res, nil
}

// Watch blocks for up to wait (at most 50 s) until a release newer than `have`
// is offered. It returns nil, nil when the time ran out; call it again.
func (c *Client) Watch(ctx context.Context, t Target, have int64, wait time.Duration) (*relproto.CheckResult, error) {
	q := checkQuery(t, have)
	if wait > 0 {
		q.Set("wait", strconv.FormatFloat(wait.Seconds(), 'f', -1, 64))
	}
	req, err := c.newRequest(ctx, http.MethodGet, "v1/apps/"+url.PathEscape(t.App)+"/watch", q, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNoContent:
		return nil, nil
	case resp.StatusCode/100 != 2:
		return nil, apiError(resp)
	}
	var res relproto.CheckResult
	if err := decodeBody(resp.Body, &res); err != nil {
		return nil, err
	}
	if err := c.settle(&res, t, have); err != nil {
		return nil, err
	}
	if !res.UpdateAvailable {
		return nil, nil
	}
	return &res, nil
}

// WatchLoop calls fn for every new release until ctx ends or fn returns an
// error. Network failures are retried with a growing pause; protocol errors
// (unknown app, bad signature, ...) end the loop.
func (c *Client) WatchLoop(ctx context.Context, t Target, have int64, fn func(*relproto.CheckResult) error) error {
	pause := c.pauseMin
	for {
		res, err := c.Watch(ctx, t, have, 45*time.Second)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil && !retryable(err):
			return err
		case err != nil:
			select {
			case <-time.After(pause):
			case <-ctx.Done():
				return ctx.Err()
			}
			pause = min(pause*2, c.pauseMax)
			continue
		}
		pause = c.pauseMin
		if res == nil {
			continue
		}
		if err := fn(res); err != nil {
			return err
		}
		have = res.Manifest.VersionCode
	}
}

// Latest returns the verified manifest a channel currently offers.
func (c *Client) Latest(ctx context.Context, t Target) (*relproto.Manifest, error) {
	var m relproto.Manifest
	q := url.Values{"channel": {t.channel()}}
	if err := c.doJSON(ctx, http.MethodGet, "v1/apps/"+url.PathEscape(t.App)+"/"+url.PathEscape(t.Platform)+"/latest", q, nil, &m); err != nil {
		return nil, err
	}
	if err := c.verify(&m); err != nil {
		return nil, err
	}
	if m.App != t.App || m.Platform != t.Platform || m.Channel != t.channel() {
		return nil, relproto.Errorf(relproto.CodeInvalid, "the server answered with the manifest of %s/%s/%s", m.App, m.Platform, m.Channel)
	}
	return &m, nil
}

// Apps lists the registered apps.
func (c *Client) Apps(ctx context.Context) ([]relproto.App, error) {
	var l relproto.AppList
	err := c.doJSON(ctx, http.MethodGet, "v1/apps", nil, nil, &l)
	return l.Apps, err
}

// App returns one app.
func (c *Client) App(ctx context.Context, id string) (relproto.App, error) {
	var a relproto.App
	err := c.doJSON(ctx, http.MethodGet, "v1/apps/"+url.PathEscape(id), nil, nil, &a)
	return a, err
}

// Releases lists a platform's releases, newest first; pass the returned next
// as before to continue. includeYanked also lists withdrawn ones.
func (c *Client) Releases(ctx context.Context, app, platform string, limit int, before int64, includeYanked bool) (list []relproto.Release, next int64, err error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if before > 0 {
		q.Set("before", strconv.FormatInt(before, 10))
	}
	if includeYanked {
		q.Set("includeYanked", "1")
	}
	var l relproto.ReleaseList
	if err := c.doJSON(ctx, http.MethodGet, "v1/apps/"+url.PathEscape(app)+"/"+url.PathEscape(platform)+"/releases", q, nil, &l); err != nil {
		return nil, 0, err
	}
	return l.Releases, l.Next, nil
}

// --- download ---

// DownloadOptions tunes Download. The zero value (or nil) is fine.
type DownloadOptions struct {
	// Progress is called after every chunk with the bytes present on disk and
	// the total; a resumed download starts above zero.
	Progress func(done, total int64)
	// Retries is how many times a download interrupted by a network error or a
	// 5xx answer continues from where it stopped. Default 3, negative = none.
	Retries int
	// RetryWait is the pause before the first retry, doubling after that. Default 1 s.
	RetryWait time.Duration
}

type statusError struct{ status int }

func (e *statusError) Error() string {
	return fmt.Sprintf("relclient: server answered HTTP %d", e.status)
}

func retryable(err error) bool {
	var se *statusError
	var ae *relproto.Error
	var ne net.Error
	switch {
	case err == nil, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case errors.As(err, &se):
		return se.status >= 500 || se.status == http.StatusTooManyRequests
	case errors.As(err, &ae):
		return ae.Code == relproto.CodeUnavailable
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF), errors.As(err, &ne):
		return true
	}
	// Transport-level failures (connection reset, refused, DNS) arrive as *url.Error.
	var ue *url.Error
	return errors.As(err, &ue)
}

// Download fetches the package of a manifest to dest. It verifies the manifest
// first, resumes a leftover dest+".part" from an earlier attempt, checks size
// and SHA-256 at the end, and only then moves the file into place, so dest is
// either absent/old or complete and verified. A package already at dest that
// matches the manifest is left alone.
func (c *Client) Download(ctx context.Context, m relproto.Manifest, dest string, opt *DownloadOptions) error {
	if err := c.verify(&m); err != nil {
		return err
	}
	var o DownloadOptions
	if opt != nil {
		o = *opt
	}
	if o.Retries == 0 {
		o.Retries = 3
	}
	if o.RetryWait <= 0 {
		o.RetryWait = time.Second
	}
	if err := checkFile(dest, &m); err == nil {
		if o.Progress != nil {
			o.Progress(m.Size, m.Size)
		}
		return nil
	}
	part := dest + ".part"
	wait, restarts := o.RetryWait, 0
	for attempt := 0; ; {
		err := c.fetch(ctx, &m, part, o.Progress)
		if err == nil {
			break
		}
		if errors.Is(err, errRestart) && restarts < 2 {
			restarts++
			continue
		}
		if !retryable(err) || attempt >= o.Retries || ctx.Err() != nil {
			return err
		}
		attempt++
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
		wait *= 2
	}
	if err := checkFile(part, &m); err != nil {
		_ = os.Remove(part)
		return err
	}
	return os.Rename(part, dest)
}

// errRestart means the partial file was discarded and the next request starts at zero.
var errRestart = errors.New("restart download")

func (c *Client) fetch(ctx context.Context, m *relproto.Manifest, part string, progress func(done, total int64)) error {
	var offset int64
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
	}
	if offset > m.Size {
		offset = 0
	}
	if offset == m.Size {
		return nil // everything is there; the caller verifies it
	}
	req, err := c.newRequest(ctx, http.MethodGet, m.DownloadPath(), nil, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		req.Header.Set("If-Range", `"`+m.SHA256+`"`)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	flags := os.O_WRONLY | os.O_CREATE
	switch {
	case resp.StatusCode == http.StatusPartialContent && offset > 0 && contentRangeStart(resp.Header.Get("Content-Range")) == offset:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC
		offset = 0
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable, resp.StatusCode == http.StatusPartialContent:
		_ = os.Remove(part)
		return errRestart
	default:
		// A 4xx answer is final; a 5xx or 429 is worth another try.
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode/100 == 2 {
			return &statusError{resp.StatusCode}
		}
		return apiError(resp)
	}
	// The server names the build it is sending; refuse another one early.
	if got := resp.Header.Get("X-Content-SHA256"); got != "" && got != m.SHA256 {
		return fmt.Errorf("%w: the server offers a different build (%s)", ErrChecksum, got)
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	done := offset
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			done += int64(n)
			if progress != nil {
				progress(done, m.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if done < m.Size {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func contentRangeStart(h string) int64 {
	rest, ok := strings.CutPrefix(h, "bytes ")
	if !ok {
		return -1
	}
	start, _, ok := strings.Cut(rest, "-")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// checkFile verifies size and SHA-256 of path against the manifest.
func checkFile(path string, m *relproto.Manifest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != m.Size {
		return fmt.Errorf("%w: size is %d, expected %d", ErrChecksum, n, m.Size)
	}
	if hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return fmt.Errorf("%w: SHA-256 differs", ErrChecksum)
	}
	return nil
}
