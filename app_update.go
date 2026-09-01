package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/coolxll/lingma-tap/internal/updater"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func updatePublicKey() ed25519.PublicKey {
	decoded, err := base64.StdEncoding.DecodeString(updater.PublicKeyBase64)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(decoded)
}

func (a *App) CheckForUpdate() (*updater.Info, error) {
	currentVersion := resolveAppVersion()
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return &updater.Info{CurrentVersion: currentVersion}, nil
	}
	client := updater.NewClient(updatePublicKey())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	candidate, err := client.Check(ctx, currentVersion, runtime.GOOS, runtime.GOARCH)
	if errors.Is(err, updater.ErrUnsupported) {
		return &candidate.Info, nil
	}
	if err != nil {
		return nil, err
	}
	a.updateMu.Lock()
	if candidate.Available {
		a.updateCandidate = candidate
	} else {
		a.updateCandidate = nil
	}
	a.updateMu.Unlock()
	return &candidate.Info, nil
}

func (a *App) InstallUpdate() error {
	a.updateMu.Lock()
	if a.updateInstalling {
		a.updateMu.Unlock()
		return fmt.Errorf("an update is already being installed")
	}
	cached := a.updateCandidate
	if cached == nil || !cached.Available {
		a.updateMu.Unlock()
		return fmt.Errorf("check for updates before installing")
	}
	a.updateInstalling = true
	a.updateMu.Unlock()

	succeeded := false
	defer func() {
		if !succeeded {
			a.updateMu.Lock()
			a.updateInstalling = false
			a.updateMu.Unlock()
		}
	}()

	client := updater.NewClient(updatePublicKey())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	candidate, err := client.Check(ctx, resolveAppVersion(), runtime.GOOS, runtime.GOARCH)
	if err != nil {
		a.emitUpdateError(err, cached.ReleaseURL)
		return err
	}
	if !candidate.Available || candidate.LatestVersion != cached.LatestVersion {
		err := fmt.Errorf("available update changed; check again")
		a.emitUpdateError(err, candidate.ReleaseURL)
		return err
	}

	if a.dataDir == "" {
		err := fmt.Errorf("application data directory is unavailable")
		a.emitUpdateError(err, candidate.ReleaseURL)
		return err
	}
	downloadDir := filepath.Join(a.dataDir, "updates", "downloads", candidate.LatestVersion)
	archivePath := filepath.Join(downloadDir, candidate.AssetName)
	a.emitUpdateProgress(updater.Progress{Phase: "downloading", TotalBytes: candidate.Size, ReleaseURL: candidate.ReleaseURL})
	lastProgress := time.Time{}
	err = client.Download(ctx, candidate, archivePath, func(downloaded, total int64) {
		if time.Since(lastProgress) < 100*time.Millisecond && downloaded < total {
			return
		}
		lastProgress = time.Now()
		a.emitUpdateProgress(updater.Progress{Phase: "downloading", DownloadedBytes: downloaded, TotalBytes: total, ReleaseURL: candidate.ReleaseURL})
	})
	if err != nil {
		a.emitUpdateError(err, candidate.ReleaseURL)
		return err
	}
	a.emitUpdateProgress(updater.Progress{Phase: "verifying", TotalBytes: candidate.Size, DownloadedBytes: candidate.Size, ReleaseURL: candidate.ReleaseURL})
	a.emitUpdateProgress(updater.Progress{Phase: "staging", ReleaseURL: candidate.ReleaseURL})
	if _, err := updater.Prepare(candidate, archivePath, a.dataDir); err != nil {
		a.emitUpdateError(err, candidate.ReleaseURL)
		return err
	}

	succeeded = true
	a.emitUpdateProgress(updater.Progress{Phase: "restarting", ReleaseURL: candidate.ReleaseURL})
	wailsruntime.Quit(a.ctx)
	return nil
}

func (a *App) emitUpdateProgress(progress updater.Progress) {
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "update:state", progress)
	}
}

func (a *App) emitUpdateError(err error, releaseURL string) {
	a.emitUpdateProgress(updater.Progress{
		Phase:          "error",
		Error:          err.Error(),
		ManualRequired: errors.Is(err, updater.ErrManualRequired),
		ReleaseURL:     releaseURL,
	})
}
