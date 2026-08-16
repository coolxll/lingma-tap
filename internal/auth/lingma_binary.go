package auth

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// LingmaBinaryError describes a failure to locate a compatible official
// Lingma language-service binary without exposing searched paths or secrets.
type LingmaBinaryError struct {
	GOOS   string
	GOARCH string
	Reason string
}

func (e *LingmaBinaryError) Error() string {
	if e == nil {
		return "Lingma service binary is unavailable"
	}
	switch e.Reason {
	case "unsupported_platform":
		return fmt.Sprintf("Lingma OAuth is not supported on %s/%s; use macOS or Windows", e.GOOS, e.GOARCH)
	case "configured_path_unusable":
		return "LINGMA_SERVICE_BINARY does not point to a usable Lingma service binary"
	default:
		return fmt.Sprintf("Lingma service binary not found for %s/%s; install Lingma or set LINGMA_SERVICE_BINARY", e.GOOS, e.GOARCH)
	}
}

// LingmaBinaryResolver locates the official service binary for one platform.
// The injectable fields make path precedence and platform behavior testable
// without mutating the host environment.
type LingmaBinaryResolver struct {
	GOOS     string
	GOARCH   string
	HomeDir  string
	Env      func(string) string
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
}

func NewLingmaBinaryResolver() LingmaBinaryResolver {
	home, _ := os.UserHomeDir()
	return LingmaBinaryResolver{
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		HomeDir:  home,
		Env:      os.Getenv,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
	}
}

// FindLingmaServiceBinary returns the official service binary, never the
// user-facing lingmacli executable.
func FindLingmaServiceBinary() (string, error) {
	return NewLingmaBinaryResolver().Resolve()
}

func (r LingmaBinaryResolver) Resolve() (string, error) {
	goos := r.GOOS
	goarch := r.GOARCH
	if goos != "darwin" && goos != "windows" {
		return "", &LingmaBinaryError{GOOS: goos, GOARCH: goarch, Reason: "unsupported_platform"}
	}
	if len(r.platformDirectories()) == 0 {
		return "", &LingmaBinaryError{GOOS: goos, GOARCH: goarch, Reason: "unsupported_platform"}
	}

	env := r.Env
	if env == nil {
		env = func(string) string { return "" }
	}
	stat := r.Stat
	if stat == nil {
		stat = os.Stat
	}
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	if configured := strings.TrimSpace(env("LINGMA_SERVICE_BINARY")); configured != "" {
		if r.usable(stat, configured) {
			return configured, nil
		}
		return "", &LingmaBinaryError{GOOS: goos, GOARCH: goarch, Reason: "configured_path_unusable"}
	}

	for _, candidate := range r.candidates(env) {
		if r.usable(stat, candidate) {
			return candidate, nil
		}
	}
	for _, name := range r.pathNames() {
		if candidate, err := lookPath(name); err == nil && r.usable(stat, candidate) {
			return candidate, nil
		}
	}
	return "", &LingmaBinaryError{GOOS: goos, GOARCH: goarch, Reason: "not_found"}
}

func (r LingmaBinaryResolver) candidates(env func(string) string) []string {
	home := r.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	platforms := r.platformDirectories()
	var candidates []string
	if r.GOOS == "darwin" {
		for _, root := range []string{"/Applications/Lingma.app", filepath.Join(home, "Applications", "Lingma.app")} {
			for _, platform := range platforms {
				candidates = append(candidates, filepath.Join(root, "Contents", "Resources", "app", "resources", "bin", platform, "Lingma"))
			}
		}
	} else {
		roots := []string{}
		if local := strings.TrimSpace(env("LOCALAPPDATA")); local != "" {
			roots = append(roots, filepath.Join(local, "Programs", "Lingma"))
		}
		if programFiles := strings.TrimSpace(env("ProgramFiles")); programFiles != "" {
			roots = append(roots, filepath.Join(programFiles, "Lingma"))
		}
		if len(roots) == 0 {
			roots = append(roots, filepath.Join(home, "AppData", "Local", "Programs", "Lingma"))
		}
		for _, root := range roots {
			for _, platform := range platforms {
				candidates = append(candidates, filepath.Join(root, "resources", "app", "resources", "bin", platform, "Lingma.exe"))
			}
		}
	}

	binRoot := filepath.Join(home, ".lingma", "bin")
	entries, _ := os.ReadDir(binRoot)
	sort.SliceStable(entries, func(i, j int) bool {
		return compareLingmaVersions(entries[i].Name(), entries[j].Name()) > 0
	})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, platform := range platforms {
			name := "Lingma"
			if r.GOOS == "windows" {
				name = "Lingma.exe"
			}
			candidates = append(candidates, filepath.Join(binRoot, entry.Name(), platform, name))
		}
	}
	return candidates
}

func (r LingmaBinaryResolver) platformDirectories() []string {
	if r.GOOS == "windows" {
		if r.GOARCH == "amd64" {
			return []string{"x86_64_windows"}
		}
		return nil
	}
	if r.GOARCH == "arm64" {
		return []string{"aarch64_darwin", "x86_64_darwin"}
	}
	if r.GOARCH == "amd64" {
		return []string{"x86_64_darwin"}
	}
	return nil
}

func (r LingmaBinaryResolver) pathNames() []string {
	if r.GOOS == "windows" {
		return []string{"Lingma.exe", "Lingma"}
	}
	return []string{"Lingma"}
}

func (r LingmaBinaryResolver) usable(stat func(string) (os.FileInfo, error), path string) bool {
	info, err := stat(path)
	if err != nil || info == nil || info.IsDir() {
		return false
	}
	if r.GOOS == "windows" {
		return strings.EqualFold(filepath.Ext(path), ".exe")
	}
	return info.Mode()&0o111 != 0
}

func compareLingmaVersions(a, b string) int {
	a = strings.TrimPrefix(strings.ToLower(a), "v")
	b = strings.TrimPrefix(strings.ToLower(b), "v")
	ap := strings.FieldsFunc(a, func(r rune) bool { return r == '.' || r == '-' || r == '_' })
	bp := strings.FieldsFunc(b, func(r rune) bool { return r == '.' || r == '-' || r == '_' })
	for i := 0; i < len(ap) && i < len(bp); i++ {
		an, aerr := strconv.Atoi(ap[i])
		bn, berr := strconv.Atoi(bp[i])
		if aerr == nil && berr == nil && an != bn {
			if an > bn {
				return 1
			}
			return -1
		}
		if ap[i] != bp[i] {
			if ap[i] > bp[i] {
				return 1
			}
			return -1
		}
	}
	if len(ap) != len(bp) {
		if len(ap) > len(bp) {
			return 1
		}
		return -1
	}
	return 0
}
