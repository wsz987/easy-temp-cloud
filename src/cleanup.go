package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

// cleanup is the externally driven expiry pass: delete objects older than the
// retention window. Run on startup and on an hourly ticker.
func (s *service) cleanup(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupLocked(ctx, time.Now().Add(-s.config.Retention))
}

// cleanupLocked is the expiry pass plus orphan reaping. Caller must hold s.mu.
// It saves the index after each expired object so a crash between deletes
// cannot leave the index pointing at removed files.
func (s *service) cleanupLocked(ctx context.Context, cutoff time.Time) error {
	for key := range s.orphans {
		if err := s.store.Delete(ctx, key); err != nil {
			return fmt.Errorf("delete unindexed object %s: %w", key, err)
		}
		delete(s.orphans, key)
	}
	for id, item := range s.records {
		if item.Created.After(cutoff) {
			continue
		}
		if err := s.store.Delete(ctx, item.ObjectKey); err != nil {
			return fmt.Errorf("delete expired object %s: %w", id, err)
		}
		delete(s.records, id)
		if err := s.saveIndexLocked(); err != nil {
			return err
		}
	}
	return nil
}

// cleanupLoop runs the expiry pass on an hourly ticker until ctx is cancelled.
func (s *service) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.cleanup(ctx); err != nil {
				log.Printf("cleanup failed: %v", err)
			}
		}
	}
}
