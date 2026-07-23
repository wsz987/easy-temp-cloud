package app

import (
	"context"
	"fmt"
	"log"
	"time"
)

const (
	cleanupInterval  = time.Minute
	cleanupBatchSize = 100
)

// cleanup is the externally driven expiry pass: delete objects older than the
// retention window. Run on startup and on a minute ticker.
func (s *service) cleanup(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupLocked(ctx, time.Now())
}

// cleanupLocked is the expiry pass plus orphan reaping. Caller must hold s.mu.
// It removes SQLite metadata after each object deletion so the cache and
// durable metadata cannot reference a removed object after a successful pass.
func (s *service) cleanupLocked(ctx context.Context, cutoff time.Time) error {
	for key := range s.orphans {
		if err := s.store.Delete(ctx, key); err != nil {
			return fmt.Errorf("delete unindexed object %s: %w", key, err)
		}
		delete(s.orphans, key)
	}
	items, err := s.metadata.expiredBefore(cutoff, cleanupBatchSize)
	if err != nil {
		return fmt.Errorf("find expired metadata: %w", err)
	}
	for _, item := range items {
		if err := s.store.Delete(ctx, item.ObjectKey); err != nil {
			return fmt.Errorf("delete expired object %s: %w", item.ID, err)
		}
		if err := s.removeRecordLocked(item); err != nil {
			return err
		}
	}
	return nil
}

// cleanupLoop runs the expiry pass on a minute ticker until ctx is cancelled.
func (s *service) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
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
