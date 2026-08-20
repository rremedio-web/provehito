// Package workspace contains path-identity and writer-lease boundaries.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/provehito-project/provehito/core/failure"
)

// CanonicalPath returns an absolute path with every existing path component
// resolved. Missing trailing components are retained after their existing
// parent has been resolved.
func CanonicalPath(path string) (string, error) {
	if path == "" {
		return "", failure.New(failure.UsageOrSchema, "workspace path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", failure.Wrap(failure.Integrity, "workspace absolute path", err)
	}
	abs = filepath.Clean(abs)
	resolved, suffix, err := resolveExistingPrefix(abs)
	if err != nil {
		return "", failure.Wrap(failure.Integrity, "workspace canonical path", err)
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	return filepath.Clean(resolved), nil
}

// SamePath reports whether two paths refer to the same filesystem object.
// Both inputs are canonicalized first. Exact cleaned equality or os.Stat plus
// os.SameFile determines identity; case-folded string comparison is not used.
func SamePath(a, b string) (bool, error) {
	ca, err := CanonicalPath(a)
	if err != nil {
		return false, err
	}
	cb, err := CanonicalPath(b)
	if err != nil {
		return false, err
	}
	if filepath.Clean(ca) == filepath.Clean(cb) {
		return true, nil
	}
	ai, err := os.Stat(ca)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, failure.Wrap(failure.Integrity, "workspace path stat", err)
	}
	bi, err := os.Stat(cb)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, failure.Wrap(failure.Integrity, "workspace path stat", err)
	}
	return os.SameFile(ai, bi), nil
}

// ValidateSeparation rejects equal, ancestor, descendant, and symlink-alias
// paths between the state root and an assigned workspace.
func ValidateSeparation(stateRoot, workspacePath string) error {
	state, err := CanonicalPath(stateRoot)
	if err != nil {
		return err
	}
	work, err := CanonicalPath(workspacePath)
	if err != nil {
		return err
	}
	same, err := SamePath(state, work)
	if err != nil {
		return err
	}
	if same {
		return failure.New(failure.Integrity, "workspace state separation")
	}
	contains, err := pathContains(state, work)
	if err != nil {
		return err
	}
	if contains {
		return failure.New(failure.Integrity, "workspace state separation")
	}
	contains, err = pathContains(work, state)
	if err != nil {
		return err
	}
	if contains {
		return failure.New(failure.Integrity, "workspace state separation")
	}
	return nil
}

// ResolveContained resolves a path against root when it is relative and
// rejects traversal or symlink traversal outside root.
func ResolveContained(root, candidate string) (string, error) {
	canonicalRoot, err := CanonicalPath(root)
	if err != nil {
		return "", err
	}
	if candidate == "" {
		return "", failure.New(failure.UsageOrSchema, "workspace contained path")
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(canonicalRoot, candidate)
	}
	resolved, err := CanonicalPath(candidate)
	if err != nil {
		return "", err
	}
	same, err := SamePath(canonicalRoot, resolved)
	if err != nil {
		return "", err
	}
	if same {
		return resolved, nil
	}
	contains, err := pathContains(canonicalRoot, resolved)
	if err != nil {
		return "", err
	}
	if !contains {
		return "", failure.New(failure.Integrity, "workspace containment")
	}
	return resolved, nil
}

func resolveExistingPrefix(path string) (string, []string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", nil, err
			}
			return resolved, suffix, nil
		}
		if !os.IsNotExist(err) {
			return "", nil, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil, fmt.Errorf("no existing path prefix for %q", path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func pathContains(parent, child string) (bool, error) {
	same, err := SamePath(parent, child)
	if err != nil {
		return false, err
	}
	if same {
		return false, nil
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false, err
	}
	if rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return true, nil
	}
	current := filepath.Clean(child)
	for {
		next := filepath.Dir(current)
		if next == current {
			return false, nil
		}
		current = next
		if _, err := os.Lstat(current); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, failure.Wrap(failure.Integrity, "workspace path stat", err)
		}
		same, err := SamePath(current, parent)
		if err != nil {
			return false, err
		}
		if same {
			return true, nil
		}
	}
}
