// Package updater implements self-update from Cloudflare R2 CDN.
//
// R2 layout:
//   dl.clawplaza.ai/clawwork/version.json              — latest version manifest
//   dl.clawplaza.ai/clawwork/v0.1.0/clawwork_0.1.0_darwin_arm64.tar.gz
//
// version.json:
//   { "version": "0.1.1", "changelog": "bug fixes" }
package updater

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const cdnBase = "https://dl.clawplaza.ai/clawwork"

// VersionInfo is the remote version manifest.
type VersionInfo struct {
	Version   string `json:"version"`
	Changelog string `json:"changelog"`
}

// CheckUpdate fetches the latest version from R2.
func CheckUpdate(current string) (*VersionInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(cdnBase + "/version.json")
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("update server returned %d", resp.StatusCode)
	}

	var info VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to parse version info: %w", err)
	}

	if !isNewer(info.Version, current) {
		return nil, nil // already up to date
	}
	return &info, nil
}

// Apply downloads the new version and replaces the current binary.
func Apply(info *VersionInfo) error {
	archiveURL := buildArchiveURL(info.Version)

	fmt.Printf("Downloading v%s ...\n", info.Version)
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(archiveURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned %d — binary may not be available yet", resp.StatusCode)
	}

	// Extract the clawwork binary from the tar.gz archive.
	newBinary, err := extractBinary(resp.Body)
	if err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}
	defer os.Remove(newBinary)

	// Replace the running binary.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate current binary: %w", err)
	}

	// Atomic replace: rename old → .bak, rename new → target, remove .bak.
	bakPath := execPath + ".bak"
	_ = os.Remove(bakPath)

	if err := os.Rename(execPath, bakPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	if err := os.Rename(newBinary, execPath); err != nil {
		// Rollback
		_ = os.Rename(bakPath, execPath)
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Preserve executable permission
	_ = os.Chmod(execPath, 0755)
	_ = os.Remove(bakPath)

	fmt.Printf("Updated to v%s\n", info.Version)
	if info.Changelog != "" {
		fmt.Printf("Changelog: %s\n", info.Changelog)
	}
	return nil
}

// buildArchiveURL returns the download URL for the current OS/arch.
// Matches GoReleaser name_template: clawwork_VERSION_OS_ARCH.tar.gz
func buildArchiveURL(ver string) string {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	ext := "tar.gz"
	if osName == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("%s/v%s/clawwork_%s_%s_%s.%s", cdnBase, ver, ver, osName, arch, ext)
}

// extractBinary reads a tar.gz stream and writes the "clawwork" binary to a temp file.
func extractBinary(r io.Reader) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar: %w", err)
		}

		name := hdr.Name
		// Match "clawwork" or "clawwork.exe" at any nesting level.
		if strings.HasSuffix(name, "clawwork") || strings.HasSuffix(name, "clawwork.exe") {
			tmp, err := os.CreateTemp("", "clawwork-update-*")
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(tmp, tr); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return "", err
			}
			tmp.Close()
			_ = os.Chmod(tmp.Name(), 0755)
			return tmp.Name(), nil
		}
	}
	return "", fmt.Errorf("clawwork binary not found in archive")
}

// isNewer returns true if remote is a higher semver than current.
// Handles "dev" as always outdated.
func isNewer(remote, current string) bool {
	if current == "dev" || current == "" {
		return true
	}
	remote = strings.TrimPrefix(remote, "v")
	current = strings.TrimPrefix(current, "v")

	rp := parseSemver(remote)
	cp := parseSemver(current)
	for i := 0; i < 3; i++ {
		if rp[i] > cp[i] {
			return true
		}
		if rp[i] < cp[i] {
			return false
		}
	}
	return false
}

func parseSemver(s string) [3]int {
	var parts [3]int
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			parts[n] = parts[n]*10 + int(c-'0')
		} else if c == '.' {
			n++
			if n >= 3 {
				break
			}
		} else {
			break // stop at pre-release suffix
		}
	}
	return parts
}
