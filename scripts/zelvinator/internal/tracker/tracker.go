// Package tracker handles deduplicated claiming of items.
package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Tracker manages a file-based set of claimed items.
type Tracker struct {
	path     string
	lockPath string
	mu       sync.Mutex
}

// NewTracker creates or opens a tracker file.
func NewTracker(dir string, name string) (*Tracker, error) {
	p := filepath.Join(dir, name)
	t := &Tracker{
		path:     p,
		lockPath: p + ".lock",
	}
	// Ensure file exists
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("cannot create tracker %s: %w", p, err)
	}
	f.Close()
	return t, nil
}

// Claim attempts to claim an item key. Returns true if newly claimed, false if already claimed.
func (t *Tracker) Claim(key string) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Acquire lock via mkdir
	if err := t.acquireLock(); err != nil {
		return false, err
	}
	defer t.releaseLock()

	// Check if already claimed (idempotent read)
	data, err := os.ReadFile(t.path)
	if err != nil {
		return false, fmt.Errorf("cannot read tracker: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == key {
			return false, nil
		}
	}

	// Append key
	f, err := os.OpenFile(t.path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return false, fmt.Errorf("cannot append to tracker: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s\n", key); err != nil {
		return false, err
	}
	return true, nil
}

// Reset clears the tracker file.
func (t *Tracker) Reset() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return os.WriteFile(t.path, []byte{}, 0644)
}

// acquireLock creates a directory-based lock.
func (t *Tracker) acquireLock() error {
	for attempt := 0; attempt < 25; attempt++ {
		if err := os.Mkdir(t.lockPath, 0755); err == nil {
			return nil
		}
	}
	return fmt.Errorf("could not acquire lock after 25 attempts")
}

// releaseLock removes the directory-based lock.
func (t *Tracker) releaseLock() {
	os.Remove(t.lockPath)
}
