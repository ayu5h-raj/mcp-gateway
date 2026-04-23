package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher observes a config file and emits freshly-parsed Configs on change.
type Watcher struct {
	path     string
	fsw      *fsnotify.Watcher
	changes  chan *Config
	errors   chan error
	done     chan struct{}
	debounce time.Duration

	mu     sync.Mutex
	closed bool
}

// NewWatcher starts watching path and emits the initial config plus any change.
// Atomic replacements (tmp + rename), which our own CLI mutations use, are handled.
func NewWatcher(path string) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify: %w", err)
	}
	// Watch the parent directory — renames emit CREATE on the new file in that dir.
	dir := filepath.Dir(path)
	if err := fsw.Add(dir); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("watch %s: %w", dir, err)
	}
	w := &Watcher{
		path:     path,
		fsw:      fsw,
		changes:  make(chan *Config, 4),
		errors:   make(chan error, 4),
		done:     make(chan struct{}),
		debounce: 150 * time.Millisecond,
	}
	// Emit initial load synchronously so the caller sees it.
	if cfg, err := ParseFile(path); err == nil {
		if err := Validate(cfg); err == nil {
			w.changes <- cfg
		} else {
			w.errors <- err
		}
	} else {
		w.errors <- err
	}
	go w.loop()
	return w, nil
}

// Changes emits freshly parsed+validated Config values.
func (w *Watcher) Changes() <-chan *Config { return w.changes }

// Errors emits parse/validation errors. Non-blocking — drops if the caller isn't draining.
func (w *Watcher) Errors() <-chan error { return w.errors }

// Close stops the watcher.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	close(w.done)
	return w.fsw.Close()
}

func (w *Watcher) loop() {
	var timer *time.Timer
	fire := make(chan struct{}, 1)
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if filepath.Clean(ev.Name) != filepath.Clean(w.path) {
				continue
			}
			// Act on any write/create/rename for our file.
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			// Debounce: coalesce bursts of fs events.
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(w.debounce, func() {
				select {
				case fire <- struct{}{}:
				default:
				}
			})
		case <-fire:
			cfg, err := ParseFile(w.path)
			if err != nil {
				w.sendErr(err)
				continue
			}
			if err := Validate(cfg); err != nil {
				w.sendErr(err)
				continue
			}
			select {
			case w.changes <- cfg:
			case <-w.done:
				return
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.sendErr(err)
		}
	}
}

func (w *Watcher) sendErr(err error) {
	select {
	case w.errors <- err:
	default:
	}
	_ = errors.Unwrap(err) // keep linter happy; we may do more later
}
