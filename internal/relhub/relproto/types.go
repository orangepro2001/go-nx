package relproto

// These are the request and response bodies of the REST API and the
// params/results of the JSON-RPC methods; both transports use the same types.

// KeyInfo describes one trusted release-signing public key.
type KeyInfo struct {
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"` // base64 Ed25519
	Active    bool   `json:"active"`    // the key that signs new manifests
}

// Info is the answer of GET /v1/info.
type Info struct {
	Name       string    `json:"name"`
	APIVersion int       `json:"apiVersion"`
	Keys       []KeyInfo `json:"keys"`
	Time       string    `json:"time"`
}

// ChannelInfo is the current release of a channel, without the signature.
type ChannelInfo struct {
	VersionCode    int64  `json:"versionCode"`
	VersionName    string `json:"versionName"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	MinVersionCode int64  `json:"minVersionCode"`
	PublishedAt    string `json:"publishedAt"`
	Notes          string `json:"notes,omitempty"`
}

// PlatformInfo lists what a platform currently offers per channel.
type PlatformInfo struct {
	Platform string                 `json:"platform"`
	Channels map[string]ChannelInfo `json:"channels"`
}

// App is a registered application.
type App struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	CreatedAt   string         `json:"createdAt"`
	Platforms   []PlatformInfo `json:"platforms"`
}

// CheckParams asks whether a newer release than VersionCode exists.
type CheckParams struct {
	App         string `json:"app"`
	Platform    string `json:"platform"`
	Channel     string `json:"channel,omitempty"` // default "stable"
	VersionCode int64  `json:"versionCode"`
}

// CheckResult is the answer to a check or to a watch that fired.
type CheckResult struct {
	App                string    `json:"app"`
	Platform           string    `json:"platform"`
	Channel            string    `json:"channel"`
	CurrentVersionCode int64     `json:"currentVersionCode"`
	UpdateAvailable    bool      `json:"updateAvailable"`
	Mandatory          bool      `json:"mandatory"`
	Manifest           *Manifest `json:"manifest"`
	DownloadPath       string    `json:"downloadPath"`
	DownloadURL        string    `json:"downloadUrl,omitempty"` // only when the server knows its public URL
}

// WaitParams is CheckParams plus how long the server may hold the request.
type WaitParams struct {
	CheckParams
	WaitSeconds float64 `json:"waitSeconds,omitempty"` // capped at MaxWatchSeconds; 0 = the maximum
}

// MaxWatchSeconds stays below the 60 s idle limit of the usual reverse proxies.
const MaxWatchSeconds = 50

// LatestParams selects the current release of one channel.
type LatestParams struct {
	App      string `json:"app"`
	Platform string `json:"platform"`
	Channel  string `json:"channel,omitempty"`
}

// ReleaseRef names one release.
type ReleaseRef struct {
	App         string `json:"app"`
	Platform    string `json:"platform"`
	VersionCode int64  `json:"versionCode"`
}

// ListReleasesParams pages through a platform's history, newest first.
type ListReleasesParams struct {
	App           string `json:"app"`
	Platform      string `json:"platform"`
	Limit         int    `json:"limit,omitempty"`  // default 50, max 200
	Before        int64  `json:"before,omitempty"` // only versionCode < Before
	IncludeYanked bool   `json:"includeYanked,omitempty"`
}

// ReleaseList is one page of releases.
type ReleaseList struct {
	Releases []Release `json:"releases"`
	Next     int64     `json:"next,omitempty"` // pass as Before for the next page; 0 = end
}

// AppRef names one app.
type AppRef struct {
	App string `json:"app"`
}

// CreateAppParams registers an application.
type CreateAppParams struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// UpdateAppParams changes an application's display data; nil fields stay.
type UpdateAppParams struct {
	App         string  `json:"app"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// PromoteParams points a channel at an existing release. MinVersionCode, if
// set, replaces the channel's minimum; Force makes this release the minimum.
type PromoteParams struct {
	App            string `json:"app"`
	Platform       string `json:"platform"`
	Channel        string `json:"channel"`
	VersionCode    int64  `json:"versionCode"`
	MinVersionCode *int64 `json:"minVersionCode,omitempty"`
	Force          bool   `json:"force,omitempty"`
}

// SetMinParams changes the minimum version of a channel.
type SetMinParams struct {
	App            string `json:"app"`
	Platform       string `json:"platform"`
	Channel        string `json:"channel"`
	MinVersionCode int64  `json:"minVersionCode"`
}

// YankParams withdraws (or restores) a release.
type YankParams struct {
	App         string `json:"app"`
	Platform    string `json:"platform"`
	VersionCode int64  `json:"versionCode"`
	Reason      string `json:"reason,omitempty"`
}

// PublishResult is the answer to a publish. Manifest is nil when the release
// was only uploaded (no channel).
type PublishResult struct {
	Release  Release   `json:"release"`
	Manifest *Manifest `json:"manifest,omitempty"`
}

// StatsParams selects which app's statistics to read; empty = all (admin only).
type StatsParams struct {
	App string `json:"app,omitempty"`
}

// DownloadStat counts the downloads of one release.
type DownloadStat struct {
	Count int64  `json:"count"`
	Last  string `json:"last,omitempty"`
}

// PlatformStats are the counters of one app on one platform.
type PlatformStats struct {
	// Downloads is keyed by versionCode.
	Downloads map[int64]DownloadStat `json:"downloads"`
	// Checks counts update checks per "channel/installedVersionCode", i.e. which
	// versions are out there.
	Checks map[string]int64 `json:"checks"`
}

// StatsResult is keyed app -> platform.
type StatsResult struct {
	Apps map[string]map[string]*PlatformStats `json:"apps"`
}

// CreateTokenParams issues an API token. Admin tokens may do everything;
// otherwise the token may publish only to the listed apps.
type CreateTokenParams struct {
	Name  string   `json:"name"`
	Apps  []string `json:"apps,omitempty"`
	Admin bool     `json:"admin,omitempty"`
}

// TokenInfo describes an issued token (never its secret).
type TokenInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Apps      []string `json:"apps,omitempty"`
	Admin     bool     `json:"admin,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

// CreateTokenResult carries the secret exactly once.
type CreateTokenResult struct {
	Token string    `json:"token"`
	Info  TokenInfo `json:"info"`
}

// TokenRef names one token.
type TokenRef struct {
	ID string `json:"id"`
}

// TokenList is the answer of token.list.
type TokenList struct {
	Tokens []TokenInfo `json:"tokens"`
}

// AppList is the answer of app.list.
type AppList struct {
	Apps []App `json:"apps"`
}

// MethodInfo describes one JSON-RPC method (rpc.discover).
type MethodInfo struct {
	Name   string `json:"name"`
	Auth   string `json:"auth"` // public | app | admin
	Doc    string `json:"doc"`
	Params string `json:"params"` // Go type name of the params object
}
