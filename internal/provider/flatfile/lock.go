package flatfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	lockRetryInterval  = 50 * time.Millisecond
	lockAcquireTimeout = 10 * time.Second
	lockStaleAfter     = 2 * time.Minute
)

type repoLock struct {
	path string
}

func (p *Provider) withGlobalLock(fn func() error) error {
	lock, err := p.acquireLock("wtp.lock")
	if err != nil {
		return err
	}
	defer lock.release()
	return fn()
}

func (p *Provider) acquireLock(name string) (repoLock, error) {
	lockPath := filepath.Join(p.root, "meta", name)
	deadline := time.Now().Add(lockAcquireTimeout)
	payload := []byte(fmt.Sprintf("pid=%d\ncreatedAt=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano)))

	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			if _, writeErr := file.Write(payload); writeErr != nil {
				file.Close()
				_ = os.Remove(lockPath)
				return repoLock{}, fmt.Errorf("write lock %s: %w", lockPath, writeErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return repoLock{}, fmt.Errorf("close lock %s: %w", lockPath, closeErr)
			}
			return repoLock{path: lockPath}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			// Windows may report ERROR_ACCESS_DENIED while another process has
			// the existing lock handle open. If the path still exists, this is
			// contention rather than a permission failure; retry through the
			// normal stale/deadline path.
			if _, statErr := os.Stat(lockPath); statErr != nil {
				// An open lock can make the file itself briefly unstatable on
				// Windows. The parent directory check distinguishes that case
				// from an actually inaccessible store.
				_, directoryErr := os.Stat(filepath.Dir(lockPath))
				if !os.IsPermission(err) || directoryErr != nil {
					return repoLock{}, fmt.Errorf("create lock %s: %w", lockPath, err)
				}
			}
		}

		stale, staleErr := lockIsStale(lockPath)
		if staleErr == nil && stale {
			if removeErr := os.Remove(lockPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return repoLock{}, fmt.Errorf("remove stale lock %s: %w", lockPath, removeErr)
			}
			continue
		}
		if time.Now().After(deadline) {
			return repoLock{}, fmt.Errorf("timed out waiting for lock %s", lockPath)
		}
		time.Sleep(lockRetryInterval)
	}
}

func lockIsStale(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	createdAt := time.Time{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "createdAt=") {
			continue
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, "createdAt="))
		if parseErr != nil {
			return false, parseErr
		}
		createdAt = parsed
		break
	}
	if !createdAt.IsZero() {
		return time.Since(createdAt) > lockStaleAfter, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return time.Since(info.ModTime()) > lockStaleAfter, nil
}

func (l repoLock) release() {
	// Windows contenders briefly hold read handles while inspecting a lock.
	// A single remove can therefore lose the release race and strand a live
	// lock until stale recovery. Retry for a short bounded interval so native
	// contention remains progress-safe without making release unbounded.
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.Remove(l.path)
		if err == nil || errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
			return
		}
		time.Sleep(lockRetryInterval)
	}
}
