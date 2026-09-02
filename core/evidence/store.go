package evidence

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rremedio-web/provehito/core/clock"
	"github.com/rremedio-web/provehito/core/failure"
	"github.com/rremedio-web/provehito/core/fingerprint"
)

// Store is an immutable content-addressed evidence store rooted outside the
// assigned workspace. Root is canonicalized when the store is constructed.
type Store struct {
	Root  string
	Clock clock.Clock
}

// maxReceiptBytes bounds the allocation made while reading a stored receipt.
// The v1 schema is deliberately bounded well below this 1 MiB ceiling.
const maxReceiptBytes = 1 << 20

// NewStore constructs a store. The optional clock is useful for deterministic
// tests; production callers use the UTC system clock by default.
func NewStore(root string, clocks ...clock.Clock) Store {
	c := clock.Clock(clock.System{})
	if len(clocks) > 0 && clocks[0] != nil {
		c = clocks[0]
	}
	if root != "" {
		abs, err := filepath.Abs(root)
		if err == nil {
			root = filepath.Clean(abs)
		}
	}
	return Store{Root: root, Clock: c}
}

// Add stamps Store-owned UTC-second time, canonicalizes the receipt, and
// installs it without overwriting a path. Re-adding equal bytes is idempotent.
func (s Store) Add(input Receipt) (Reference, error) {
	if s.Root == "" {
		return Reference{}, failure.New(failure.UsageOrSchema, "evidence store root")
	}
	if err := input.validateInput(); err != nil {
		return Reference{}, failure.Wrap(failure.UsageOrSchema, "evidence receipt", err)
	}
	input = cloneReceipt(input)
	input.Timestamp = s.now().UTC().Truncate(time.Second).Format(time.RFC3339)
	input.CanonicalHash = ""
	hash, err := canonicalHash(input)
	if err != nil {
		return Reference{}, failure.Wrap(failure.UsageOrSchema, "evidence receipt hash", err)
	}
	input.CanonicalHash = hash
	data, err := receiptBytes(input)
	if err != nil {
		return Reference{}, failure.Wrap(failure.UsageOrSchema, "evidence receipt encode", err)
	}
	path, err := s.pathFor(hash)
	if err != nil {
		return Reference{}, err
	}
	if err := s.ensureParents(filepath.Dir(path)); err != nil {
		return Reference{}, err
	}
	if err := s.install(path, data); err != nil {
		return Reference{}, err
	}
	return Reference{Hash: hash, Path: path}, nil
}

// Load reads, validates, and returns a receipt identified by ref.
func (s Store) Load(ref Reference) (Receipt, error) {
	path, err := s.validateReference(ref)
	if err != nil {
		return Receipt{}, err
	}
	data, err := readPrivateFile(path)
	if err != nil {
		return Receipt{}, failure.Wrap(failure.Integrity, "evidence load", err)
	}
	receipt, err := decodeReceipt(data)
	if err != nil {
		return Receipt{}, err
	}
	if receipt.CanonicalHash != ref.Hash {
		return Receipt{}, failure.New(failure.Integrity, "evidence reference hash")
	}
	return receipt, nil
}

// Verify checks only the immutable bytes and identity represented by ref.
func (s Store) Verify(ref Reference) error {
	_, err := s.Load(ref)
	return err
}

func cloneReceipt(r Receipt) Receipt {
	r = normalizeIdentity(r)
	r.Artifacts = cloneReferences(r.Artifacts)
	r.Candidate = fingerprint.Fingerprint{}
	return r
}

func (s Store) now() time.Time {
	if s.Clock == nil {
		return clock.System{}.Now()
	}
	return s.Clock.Now().UTC()
}

func (s Store) pathFor(hash string) (string, error) {
	if !failure.IsHash(hash) {
		return "", failure.New(failure.UsageOrSchema, "evidence hash")
	}
	root, err := s.rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "evidence", "sha256", hash[:2], hash+".json"), nil
}

func (s Store) rootPath() (string, error) {
	if s.Root == "" || !filepath.IsAbs(s.Root) {
		return "", failure.New(failure.UsageOrSchema, "evidence absolute root")
	}
	if info, err := os.Lstat(s.Root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", failure.New(failure.Integrity, "evidence root identity")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", failure.Wrap(failure.Integrity, "evidence root stat", err)
	}
	return filepath.Clean(s.Root), nil
}

func (s Store) ensureParents(parent string) error {
	root, err := s.rootPath()
	if err != nil {
		return err
	}
	if err := ensurePrivateDir(root); err != nil {
		return failure.Wrap(failure.Integrity, "evidence root", err)
	}
	current := root
	rel, err := filepath.Rel(root, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return failure.New(failure.Integrity, "evidence path containment")
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if info, statErr := os.Lstat(current); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return failure.New(failure.Integrity, "evidence directory identity")
			}
			if err := os.Chmod(current, 0o700); err != nil {
				return failure.Wrap(failure.Integrity, "evidence directory mode", err)
			}
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return failure.Wrap(failure.Integrity, "evidence directory stat", statErr)
		}
		if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return failure.Wrap(failure.Integrity, "evidence directory create", err)
		}
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return failure.New(failure.Integrity, "evidence directory race")
		}
		if err := os.Chmod(current, 0o700); err != nil {
			return failure.Wrap(failure.Integrity, "evidence directory mode", err)
		}
	}
	return nil
}

func ensurePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("not a private directory")
	}
	return os.Chmod(path, 0o700)
}

func (s Store) validateReference(ref Reference) (string, error) {
	if !failure.IsHash(ref.Hash) {
		return "", failure.New(failure.Integrity, "evidence reference hash")
	}
	path, err := s.pathFor(ref.Hash)
	if err != nil {
		if failure.Is(err, failure.UsageOrSchema) {
			return "", failure.Wrap(failure.Integrity, "evidence reference path", err)
		}
		return "", err
	}
	if ref.Path != "" && ref.Path != path {
		return "", failure.New(failure.Integrity, "evidence reference path")
	}
	if err := s.checkParents(filepath.Dir(path)); err != nil {
		return "", err
	}
	return path, nil
}

func (s Store) checkParents(parent string) error {
	root, err := s.rootPath()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return failure.New(failure.Integrity, "evidence path containment")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return failure.Wrap(failure.Integrity, "evidence root stat", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o700 {
		return failure.New(failure.Integrity, "evidence root identity")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return failure.Wrap(failure.Integrity, "evidence parent stat", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
			return failure.New(failure.Integrity, "evidence parent identity")
		}
	}
	return nil
}

func readPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("evidence file identity or mode")
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
	if !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm() != 0o600 || openedInfo.Size() < 0 || openedInfo.Size() > maxReceiptBytes {
		return nil, fmt.Errorf("evidence file size or identity")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxReceiptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxReceiptBytes {
		return nil, fmt.Errorf("evidence file size exceeds maximum")
	}
	return data, nil
}

func (s Store) install(path string, data []byte) error {
	parent := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(parent, "."+base+".tmp-*")
	if err != nil {
		return failure.Wrap(failure.Integrity, "evidence temporary create", err)
	}
	tmpName := tmp.Name()
	remove := true
	defer func() {
		_ = tmp.Close()
		if remove {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return failure.Wrap(failure.Integrity, "evidence temporary mode", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return failure.Wrap(failure.Integrity, "evidence temporary write", err)
	}
	if err := tmp.Sync(); err != nil {
		return failure.Wrap(failure.Integrity, "evidence temporary sync", err)
	}
	if err := tmp.Close(); err != nil {
		return failure.Wrap(failure.Integrity, "evidence temporary close", err)
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return s.compareExisting(path, data)
		}
		return failure.Wrap(failure.Integrity, "evidence install", err)
	}
	if err := os.Remove(tmpName); err != nil {
		return failure.Wrap(failure.Integrity, "evidence temporary cleanup", err)
	}
	remove = false
	if err := syncDir(parent); err != nil {
		return failure.Wrap(failure.Integrity, "evidence directory sync", err)
	}
	return nil
}

func (s Store) compareExisting(path string, want []byte) error {
	got, err := readPrivateFile(path)
	if err != nil {
		return failure.Wrap(failure.Integrity, "evidence existing path", err)
	}
	if !bytes.Equal(got, want) {
		return failure.New(failure.Integrity, "evidence content collision")
	}
	return nil
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// noFollowFlag is kept in one place so opening a target cannot follow a
// symlink after the preceding Lstat check.
func noFollowFlag() int {
	return syscall.O_NOFOLLOW
}
