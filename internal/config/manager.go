package config

import (
	"context"
	"log"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Manager struct {
	path    string
	watcher *fsnotify.Watcher

	mu      sync.RWMutex
	current *AppConfig

	updates chan *AppConfig
	cancel  context.CancelFunc
}

func NewManager(path string) (*Manager, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig(absPath)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		path:    absPath,
		watcher: watcher,
		current: cfg,
		updates: make(chan *AppConfig, 1),
		cancel:  cancel,
	}

	if err := watcher.Add(filepath.Dir(absPath)); err != nil {
		watcher.Close()
		cancel()
		return nil, err
	}

	go manager.loop(ctx)
	return manager, nil
}

func (m *Manager) loop(ctx context.Context) {
	defer close(m.updates)
	for {
		select {
		case ev := <-m.watcher.Events:
			if ev.Name == "" {
				continue
			}
			if filepath.Clean(ev.Name) != m.path {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
				if cfg, err := LoadConfig(m.path); err == nil {
					m.mu.Lock()
					m.current = cfg
					m.mu.Unlock()
					select {
					case m.updates <- cfg:
					default:
						log.Printf("[config] dropping update event, slow consumer")
					}
					log.Printf("[config] reloaded schema=%s strategy=%s", cfg.SchemaVersion, cfg.StrategyVersion)
				} else {
					log.Printf("[config] reload failed: %v", err)
				}
			}
		case err := <-m.watcher.Errors:
			if err != nil {
				log.Printf("[config] watcher error: %v", err)
			}
		case <-ctx.Done():
			_ = m.watcher.Close()
			return
		}
	}
}

func (m *Manager) Current() *AppConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

func (m *Manager) Updates() <-chan *AppConfig {
	return m.updates
}

func (m *Manager) Close() {
	m.cancel()
}
