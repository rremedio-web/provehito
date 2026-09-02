package manifest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rremedio-web/provehito/core/clock"
	"github.com/rremedio-web/provehito/core/failure"
)

// maxManifestBytes bounds the allocation made while reading a stored manifest.
const maxManifestBytes = 1 << 20

// Store persists one manifest at Path and never silently repairs it.
type Store struct {
	Path  string
	Clock clock.Clock
}

// NewStore constructs a store with an injectable clock.
func NewStore(path string, c clock.Clock) Store { return Store{Path: path, Clock: c} }

// Create writes a new manifest and refuses to replace an existing path.
func (s Store) Create(m Manifest) (string, error) {
	if s.Path == "" {
		return "", failure.New(failure.UsageOrSchema, "manifest store path")
	}
	lock, err := lockManifest(s.Path)
	if err != nil {
		return "", failure.Wrap(failure.Integrity, "manifest create lock", err)
	}
	defer lock.Close()
	if err := rejectExistingFinalEntry(s.Path); err != nil {
		return "", err
	}
	now := s.now().Format(time.RFC3339)
	m.CreatedAt = now
	m.UpdatedAt = now
	m, data, hash, err := normalize(m)
	if err != nil {
		return "", failure.Wrap(failure.UsageOrSchema, "manifest create", err)
	}
	if err := validate(m); err != nil {
		return "", err
	}
	if err := writeExclusive(s.Path, data); err != nil {
		return "", err
	}
	return hash, nil
}

// Load reads and verifies the canonical manifest and returns its content hash.
func (s Store) Load() (Manifest, string, error) {
	if s.Path == "" {
		return Manifest{}, "", failure.New(failure.UsageOrSchema, "manifest store path")
	}
	data, err := readManifestFile(s.Path)
	if err != nil {
		return Manifest{}, "", failure.Wrap(failure.Integrity, "manifest load", err)
	}
	return decode(data)
}

// Update replaces a manifest only when expectedHash matches the current
// canonical bytes. An empty or stale expected hash is never a wildcard.
func (s Store) Update(expectedHash string, m Manifest) (string, error) {
	if expectedHash == "" {
		return "", failure.New(failure.Integrity, "manifest update missing prior hash")
	}
	lock, err := lockManifest(s.Path)
	if err != nil {
		return "", failure.Wrap(failure.Integrity, "manifest update lock", err)
	}
	defer lock.Close()
	current, currentHash, err := s.Load()
	if err != nil {
		return "", err
	}
	if currentHash != expectedHash {
		return "", failure.New(failure.Integrity, "manifest update stale prior hash")
	}
	m.CreatedAt = current.CreatedAt
	m.UpdatedAt = s.now().Format(time.RFC3339)
	var data []byte
	var hash string
	m, data, hash, err = normalize(m)
	if err != nil {
		return "", failure.Wrap(failure.UsageOrSchema, "manifest update", err)
	}
	if err := ValidateUpdate(currentHash, current, m); err != nil {
		return "", err
	}
	if err := validate(m); err != nil {
		return "", err
	}
	if err := writeAtomic(s.Path, data); err != nil {
		return "", err
	}
	return hash, nil
}

func (s Store) now() time.Time {
	if s.Clock == nil {
		return clock.System{}.Now()
	}
	return s.Clock.Now().UTC()
}

func rejectExistingFinalEntry(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return failure.Wrap(failure.Integrity, "manifest create stat", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return failure.New(failure.Integrity, "manifest path identity")
	}
	if !info.Mode().IsRegular() {
		return failure.New(failure.Integrity, "manifest path identity")
	}
	return failure.New(failure.Integrity, "manifest already exists")
}

func readManifestFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("manifest file identity or mode")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag(), 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("manifest file identity")
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm() != 0600 || openedInfo.Size() < 0 || openedInfo.Size() > maxManifestBytes {
		return nil, fmt.Errorf("manifest file size or identity")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("manifest file size exceeds maximum")
	}
	return data, nil
}

func writeExclusive(path string, data []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if base == "." || base == ".." || strings.Contains(base, string(filepath.Separator)) {
		return failure.New(failure.UsageOrSchema, "manifest path")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return failure.Wrap(failure.Integrity, "manifest residue scan", err)
	}
	prefix := "." + base + ".tmp-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			return failure.New(failure.Integrity, "manifest temporary residue")
		}
	}
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary create", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary chmod", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary write", err)
	}
	if err := tmp.Sync(); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary sync", err)
	}
	if err := tmp.Close(); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary close", err)
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return failure.New(failure.Integrity, "manifest already exists")
		}
		return failure.Wrap(failure.Integrity, "manifest exclusive install", err)
	}
	if err := os.Remove(tmpName); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary cleanup", err)
	}
	removeTemp = false
	d, err := os.Open(dir)
	if err != nil {
		return failure.Wrap(failure.Integrity, "manifest directory open", err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return failure.Wrap(failure.Integrity, "manifest directory sync", err)
	}
	if err := d.Close(); err != nil {
		return failure.Wrap(failure.Integrity, "manifest directory close", err)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if base == "." || base == ".." || strings.Contains(base, string(filepath.Separator)) {
		return failure.New(failure.UsageOrSchema, "manifest path")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return failure.Wrap(failure.Integrity, "manifest residue scan", err)
	}
	prefix := "." + base + ".tmp-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			return failure.New(failure.Integrity, "manifest temporary residue")
		}
	}
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary create", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary chmod", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary write", err)
	}
	if err := tmp.Sync(); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary sync", err)
	}
	if err := tmp.Close(); err != nil {
		return failure.Wrap(failure.Integrity, "manifest temporary close", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return failure.Wrap(failure.Integrity, "manifest atomic rename", err)
	}
	removeTemp = false
	d, err := os.Open(dir)
	if err != nil {
		return failure.Wrap(failure.Integrity, "manifest directory open", err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return failure.Wrap(failure.Integrity, "manifest directory sync", err)
	}
	if err := d.Close(); err != nil {
		return failure.Wrap(failure.Integrity, "manifest directory close", err)
	}
	return nil
}
