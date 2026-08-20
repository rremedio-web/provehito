package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/provehito-project/provehito/core/failure"
)

// LeaseManager coordinates leases in one explicit state root.
type LeaseManager struct {
	Root string
}

// Lease is an exclusive writer lease. Its lock remains held until Release.
type Lease struct {
	LaneID            string
	PID               int
	ProcessStartTime  time.Time
	WorkspaceIdentity string
	AcquiredAt        time.Time

	manager      *LeaseManager
	lock         *os.File
	lockPath     string
	leasePath    string
	workspaceDev uint64
	workspaceIno uint64
	mu           sync.Mutex
	released     bool
}

type leaseRecord struct {
	LaneID            string `json:"lane_id"`
	PID               int    `json:"pid"`
	ProcessStartTime  string `json:"process_start_time"`
	WorkspaceIdentity string `json:"workspace_identity"`
	WorkspaceDev      uint64 `json:"workspace_dev"`
	WorkspaceIno      uint64 `json:"workspace_ino"`
	AcquiredAt        string `json:"acquired_at"`
}

type workspaceObjectID struct {
	Dev uint64
	Ino uint64
}

// NewLeaseManager constructs a manager rooted at stateRoot.
func NewLeaseManager(stateRoot string) *LeaseManager {
	return &LeaseManager{Root: stateRoot}
}

// Acquire takes the one writer lease for workspace.
func (m *LeaseManager) Acquire(laneID, workspacePath string) (*Lease, error) {
	if m == nil || m.Root == "" || laneID == "" || workspacePath == "" {
		return nil, failure.New(failure.UsageOrSchema, "workspace lease arguments")
	}
	if err := ValidateSeparation(m.Root, workspacePath); err != nil {
		return nil, err
	}
	identity, err := CanonicalPath(workspacePath)
	if err != nil {
		return nil, err
	}
	objectID, err := statWorkspaceObject(identity)
	if err != nil {
		return nil, err
	}
	root, err := CanonicalPath(m.Root)
	if err != nil {
		return nil, err
	}
	key := workspaceKeyFromObjectID(objectID)
	lockPath := filepath.Join(root, key+".lock")
	leasePath := filepath.Join(root, key+".lease")
	lock, err := openLeaseLock(lockPath)
	if err != nil {
		return nil, failure.Wrap(failure.Integrity, "workspace lease lock", err)
	}
	if err := tryLock(lock); err != nil {
		_ = lock.Close()
		if errors.Is(err, errLockBusy) {
			return nil, failure.Wrap(failure.Concurrency, "workspace writer lease", err)
		}
		return nil, failure.Wrap(failure.Integrity, "workspace writer lease", err)
	}

	if exists, err := existingLease(leasePath); err != nil {
		unlockAndClose(lock)
		return nil, err
	} else if exists {
		unlockAndClose(lock)
		return nil, failure.New(failure.Concurrency, "workspace abandoned lease")
	}

	processStart := processStartTime()
	acquired := time.Now().UTC()
	record := leaseRecord{
		LaneID:            laneID,
		PID:               os.Getpid(),
		ProcessStartTime:  processStart.Format(time.RFC3339Nano),
		WorkspaceIdentity: identity,
		WorkspaceDev:      objectID.Dev,
		WorkspaceIno:      objectID.Ino,
		AcquiredAt:        acquired.Format(time.RFC3339Nano),
	}
	if err := writeLease(leasePath, record); err != nil {
		unlockAndClose(lock)
		return nil, err
	}
	return &Lease{
		LaneID:            laneID,
		PID:               record.PID,
		ProcessStartTime:  processStart,
		WorkspaceIdentity: identity,
		AcquiredAt:        acquired,
		manager:           m,
		lock:              lock,
		lockPath:          lockPath,
		leasePath:         leasePath,
		workspaceDev:      objectID.Dev,
		workspaceIno:      objectID.Ino,
	}, nil
}

// ActiveWorkspace returns the canonical workspace only while this lease still
// has its private lock and durable record. A zero-value or released lease is
// never a valid witness.
func (l *Lease) ActiveWorkspace() (string, bool) {
	if l == nil {
		return "", false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released || l.manager == nil || l.lock == nil || l.lockPath == "" || l.leasePath == "" || l.WorkspaceIdentity == "" {
		return "", false
	}
	lockInfo, err := os.Lstat(l.lockPath)
	if err != nil || lockInfo.Mode()&os.ModeSymlink != 0 || !lockInfo.Mode().IsRegular() {
		return "", false
	}
	leaseInfo, err := os.Lstat(l.leasePath)
	if err != nil || leaseInfo.Mode()&os.ModeSymlink != 0 || !leaseInfo.Mode().IsRegular() {
		return "", false
	}
	current, err := statWorkspaceObject(l.WorkspaceIdentity)
	if err != nil || current.Dev != l.workspaceDev || current.Ino != l.workspaceIno {
		return "", false
	}
	return l.WorkspaceIdentity, true
}

// Release removes the durable record and releases the OS lock.
func (l *Lease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	var releaseErr error
	if err := rejectSymlink(l.leasePath); err != nil {
		releaseErr = failure.Wrap(failure.Integrity, "workspace lease release", err)
	} else if err := os.Remove(l.leasePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		releaseErr = failure.Wrap(failure.Integrity, "workspace lease release", err)
	} else if err == nil {
		if err := syncParent(l.leasePath); err != nil {
			releaseErr = failure.Wrap(failure.Integrity, "workspace lease release sync", err)
		}
	}
	unlockErr := unlockAndClose(l.lock)
	l.released = true
	if releaseErr != nil {
		return releaseErr
	}
	if unlockErr != nil {
		return failure.Wrap(failure.Integrity, "workspace lease unlock", unlockErr)
	}
	return nil
}

// DetectAbandoned reports a durable lease whose OS lock is no longer held.
// The durable record is intentionally retained so reuse remains blocked.
func (m *LeaseManager) DetectAbandoned(workspacePath string) error {
	if m == nil || m.Root == "" || workspacePath == "" {
		return failure.New(failure.UsageOrSchema, "workspace abandoned lease arguments")
	}
	if err := ValidateSeparation(m.Root, workspacePath); err != nil {
		return err
	}
	root, err := CanonicalPath(m.Root)
	if err != nil {
		return err
	}
	key, err := workspaceKey(workspacePath)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(root, key+".lock")
	leasePath := filepath.Join(root, key+".lease")
	exists, err := existingLease(leasePath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	lock, err := openLeaseLock(lockPath)
	if err != nil {
		return failure.Wrap(failure.Integrity, "workspace abandoned lock", err)
	}
	defer lock.Close()
	if err := tryLock(lock); err != nil {
		if errors.Is(err, errLockBusy) {
			return nil
		}
		return failure.Wrap(failure.Integrity, "workspace abandoned lock", err)
	}
	_ = syscallUnlock(lock)
	return failure.New(failure.Concurrency, "workspace abandoned lease")
}

func workspaceKey(workspacePath string) (string, error) {
	canonical, err := CanonicalPath(workspacePath)
	if err != nil {
		return "", err
	}
	objectID, err := statWorkspaceObject(canonical)
	if err != nil {
		return "", err
	}
	return workspaceKeyFromObjectID(objectID), nil
}

func workspaceKeyFromObjectID(objectID workspaceObjectID) string {
	payload := fmt.Sprintf("%d:%d", objectID.Dev, objectID.Ino)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func statWorkspaceObject(canonical string) (workspaceObjectID, error) {
	info, err := os.Stat(canonical)
	if err != nil {
		return workspaceObjectID{}, failure.Wrap(failure.Integrity, "workspace identity stat", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return workspaceObjectID{}, failure.New(failure.Integrity, "workspace identity stat")
	}
	return workspaceObjectID{Dev: uint64(stat.Dev), Ino: stat.Ino}, nil
}

func existingLease(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, failure.Wrap(failure.Integrity, "workspace lease stat", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, failure.New(failure.Integrity, "workspace lease symlink")
	}
	return true, nil
}

func writeLease(path string, record leaseRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return failure.Wrap(failure.Integrity, "workspace lease encode", err)
	}
	file, err := openExclusive(path)
	if err != nil {
		return failure.Wrap(failure.Integrity, "workspace lease create", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return failure.Wrap(failure.Integrity, "workspace lease chmod", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return failure.Wrap(failure.Integrity, "workspace lease write", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return failure.Wrap(failure.Integrity, "workspace lease sync", err)
	}
	if err := file.Close(); err != nil {
		return failure.Wrap(failure.Integrity, "workspace lease close", err)
	}
	if err := syncParent(path); err != nil {
		return failure.Wrap(failure.Integrity, "workspace lease directory sync", err)
	}
	return nil
}

func processStartTime() time.Time {
	return processStartedAt
}

var (
	processStartedAt = time.Now().UTC()
)

var errLockBusy = fmt.Errorf("writer lock is busy")
