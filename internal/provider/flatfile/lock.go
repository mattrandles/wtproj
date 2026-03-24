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
			return repoLock{}, fmt.Errorf("create lock %s: %w", lockPath, err)
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
	_ = os.Remove(l.path)
}
