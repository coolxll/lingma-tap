package updater

import "errors"

const (
	DefaultRepository = "coolxll/lingma-tap"
	ManifestName      = "update-manifest.json"
	SignatureName     = "update-manifest.json.sig"
	PublicKeyBase64   = "mVzzxob6ZQtN0KLBK8Dhs4Al1znASZVORgs5DyVU65s="
)

var (
	ErrUnsupported      = errors.New("automatic updates are not supported for this build")
	ErrManualRequired   = errors.New("the application location is not writable; manual update required")
	ErrNoSignedManifest = errors.New("release does not contain a signed update manifest")
)

type Manifest struct {
	Version string          `json:"version"`
	Assets  []ManifestAsset `json:"assets"`
}

type ManifestAsset struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Info struct {
	Supported      bool   `json:"supported"`
	Available      bool   `json:"available"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	ReleaseURL     string `json:"release_url"`
	AssetName      string `json:"asset_name"`
}

type Candidate struct {
	Info
	DownloadURL string
	Size        int64
	SHA256      string
}

type Progress struct {
	Phase           string `json:"phase"`
	DownloadedBytes int64  `json:"downloaded_bytes,omitempty"`
	TotalBytes      int64  `json:"total_bytes,omitempty"`
	Error           string `json:"error,omitempty"`
	ManualRequired  bool   `json:"manual_required,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
}
