package relclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/orangepro2001/go-nx/internal/relhub/relproto"
)

// PublishRequest describes a release to upload. Set Path, or File with FileName.
type PublishRequest struct {
	App         string
	Platform    string
	VersionCode int64
	VersionName string
	Notes       string

	// Channel the release goes live on; empty means "stable". NoChannel uploads
	// the release without offering it anywhere (promote it later).
	Channel   string
	NoChannel bool
	// MinVersionCode > 0 sets the channel's minimum; Force makes this release the minimum.
	MinVersionCode int64
	Force          bool

	// Path is the package on disk. Its SHA-256 is computed and sent along so the
	// server can verify the transfer.
	Path string
	// File and FileName are the alternative to Path for packages that are not files.
	File     io.Reader
	FileName string
	// StoredName is the name the package is stored and downloaded under; default is FileName or the base of Path.
	StoredName string
	// SHA256 is the expected digest (hex). Computed from Path if empty.
	SHA256 string

	// Progress reports the bytes of the package sent so far.
	Progress func(sent int64)
	// SkipPreflight turns off the cheap checks made before the upload starts.
	SkipPreflight bool
}

type countingReader struct {
	r    io.Reader
	n    int64
	prog func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.prog != nil && n > 0 {
		c.prog(c.n)
	}
	return n, err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// preflight reports a rejected upload before the package is sent: a transfer
// that the server turns away midway often surfaces as a reset connection
// instead of the reason.
func (c *Client) preflight(ctx context.Context, req *PublishRequest) error {
	if _, err := c.Stats(ctx, req.App); err != nil { // needs a token that may touch the app
		return err
	}
	list, _, err := c.Releases(ctx, req.App, req.Platform, 1, 0, true)
	switch {
	case relprotoCode(err) == relproto.CodeNotFound:
		if _, aerr := c.App(ctx, req.App); aerr != nil {
			return aerr // the app itself does not exist
		}
		return nil // first release on this platform
	case err != nil:
		return err
	case len(list) > 0 && req.VersionCode <= list[0].VersionCode:
		return relproto.Errorf(relproto.CodeConflict, "versionCode %d is not higher than the published %d", req.VersionCode, list[0].VersionCode)
	}
	return nil
}

func relprotoCode(err error) string {
	if err == nil {
		return ""
	}
	return relproto.AsError(err).Code
}

// Publish uploads a release. The package is streamed, never held in memory.
func (c *Client) Publish(ctx context.Context, req PublishRequest) (*relproto.PublishResult, error) {
	src, name := req.File, req.FileName
	if req.Path != "" {
		f, err := os.Open(req.Path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		src, name = f, filepath.Base(req.Path)
		if req.SHA256 == "" {
			if req.SHA256, err = fileSHA256(req.Path); err != nil {
				return nil, err
			}
		}
	}
	if src == nil {
		return nil, fmt.Errorf("relclient: Publish needs Path or File")
	}
	if name == "" {
		name = "package.bin"
	}
	if !req.SkipPreflight {
		if err := c.preflight(ctx, &req); err != nil {
			return nil, err
		}
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			fields := [][2]string{{"versionCode", strconv.FormatInt(req.VersionCode, 10)}, {"versionName", req.VersionName}}
			add := func(k, v string) { fields = append(fields, [2]string{k, v}) }
			if req.Notes != "" {
				add("notes", req.Notes)
			}
			switch {
			case req.NoChannel:
				add("channel", "")
			case req.Channel != "":
				add("channel", req.Channel)
			}
			if req.MinVersionCode > 0 {
				add("minVersionCode", strconv.FormatInt(req.MinVersionCode, 10))
			}
			if req.Force {
				add("force", "true")
			}
			if req.SHA256 != "" {
				add("sha256", req.SHA256)
			}
			if req.StoredName != "" {
				add("fileName", req.StoredName)
			}
			for _, kv := range fields {
				if err := mw.WriteField(kv[0], kv[1]); err != nil {
					return err
				}
			}
			w, err := mw.CreateFormFile("file", name)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, &countingReader{r: src, prog: req.Progress})
			return err
		}()
		if cerr := mw.Close(); err == nil {
			err = cerr
		}
		_ = pw.CloseWithError(err)
	}()

	hreq, err := c.newRequest(ctx, http.MethodPost, "v1/apps/"+url.PathEscape(req.App)+"/"+url.PathEscape(req.Platform)+"/releases", nil, pr)
	if err != nil {
		_ = pr.Close()
		return nil, err
	}
	hreq.Header.Set("Content-Type", mw.FormDataContentType())
	hreq.Header.Set("Accept", "application/json")
	// Let the server refuse (bad token, wrong app) before the package is sent.
	hreq.Header.Set("Expect", "100-continue")
	resp, err := c.http.Do(hreq)
	if err != nil {
		_ = pr.CloseWithError(err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, apiError(resp)
	}
	var res relproto.PublishResult
	if err := decodeBody(resp.Body, &res); err != nil {
		return nil, err
	}
	if res.Manifest != nil {
		if err := c.verify(res.Manifest); err != nil {
			return nil, err
		}
	}
	return &res, nil
}

// --- management ---

func appPath(app, platform string, rest ...string) string {
	p := "v1/apps/" + url.PathEscape(app)
	if platform != "" {
		p += "/" + url.PathEscape(platform)
	}
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// CreateApp registers an app (admin token).
func (c *Client) CreateApp(ctx context.Context, p relproto.CreateAppParams) (relproto.App, error) {
	var a relproto.App
	err := c.doJSON(ctx, http.MethodPost, "v1/apps", nil, p, &a)
	return a, err
}

// UpdateApp changes an app's name or description (admin token).
func (c *Client) UpdateApp(ctx context.Context, p relproto.UpdateAppParams) (relproto.App, error) {
	var a relproto.App
	err := c.doJSON(ctx, http.MethodPatch, appPath(p.App, ""), nil, p, &a)
	return a, err
}

// Promote points a channel at an existing release (app or admin token).
func (c *Client) Promote(ctx context.Context, p relproto.PromoteParams) (relproto.Manifest, error) {
	var m relproto.Manifest
	err := c.doJSON(ctx, http.MethodPost, appPath(p.App, p.Platform, "channels", url.PathEscape(p.Channel)), nil, p, &m)
	return m, err
}

// SetMin changes a channel's minimum version (app or admin token).
func (c *Client) SetMin(ctx context.Context, p relproto.SetMinParams) (relproto.Manifest, error) {
	var m relproto.Manifest
	err := c.doJSON(ctx, http.MethodPatch, appPath(p.App, p.Platform, "channels", url.PathEscape(p.Channel)), nil, p, &m)
	return m, err
}

// Yank withdraws a release (app or admin token).
func (c *Client) Yank(ctx context.Context, p relproto.YankParams) (relproto.Release, error) {
	var r relproto.Release
	err := c.doJSON(ctx, http.MethodPost, appPath(p.App, p.Platform, "releases", strconv.FormatInt(p.VersionCode, 10), "yank"), nil, p, &r)
	return r, err
}

// Unyank restores a withdrawn release (app or admin token).
func (c *Client) Unyank(ctx context.Context, p relproto.YankParams) (relproto.Release, error) {
	var r relproto.Release
	err := c.doJSON(ctx, http.MethodPost, appPath(p.App, p.Platform, "releases", strconv.FormatInt(p.VersionCode, 10), "unyank"), nil, nil, &r)
	return r, err
}

// Stats returns the counters of an app, or of all apps if app is empty (admin token).
func (c *Client) Stats(ctx context.Context, app string) (relproto.StatsResult, error) {
	var s relproto.StatsResult
	q := url.Values{}
	if app != "" {
		q.Set("app", app)
	}
	err := c.doJSON(ctx, http.MethodGet, "v1/stats", q, nil, &s)
	return s, err
}

// CreateToken issues an API token (admin token). The secret is in the result, once.
func (c *Client) CreateToken(ctx context.Context, p relproto.CreateTokenParams) (relproto.CreateTokenResult, error) {
	var r relproto.CreateTokenResult
	err := c.doJSON(ctx, http.MethodPost, "v1/tokens", nil, p, &r)
	return r, err
}

// Tokens lists issued tokens (admin token).
func (c *Client) Tokens(ctx context.Context) ([]relproto.TokenInfo, error) {
	var l relproto.TokenList
	err := c.doJSON(ctx, http.MethodGet, "v1/tokens", nil, nil, &l)
	return l.Tokens, err
}

// RevokeToken deletes a token (admin token).
func (c *Client) RevokeToken(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "v1/tokens/"+url.PathEscape(id), nil, nil, nil)
}
