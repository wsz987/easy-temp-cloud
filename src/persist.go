//go:build ignore

package main

import (
	"context"
	"errors"
	"log"
	"time"
)

var (
	errTooLarge    = errors.New("file too large")
	errStorageFull = errors.New("storage capacity exceeded")
)

// persist writes a fully received object to the store and commits its metadata.
// If an object with the same SHA-256 already exists it returns the existing
// record as a duplicate without re-writing it. The caller must have reserved
// `reservation` bytes and is responsible for releasing them afterwards.
func (s *service) persist(ctx context.Context, id, sourcePath, contentType string, size, reservation int64) (record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.cleanupLocked(ctx, time.Now().Add(-s.config.Retention)); err != nil {
		return record{}, false, err
	}
	if existing, ok := s.records[id]; ok {
		return existing, true, nil
	}
	otherReservations := s.reservedBytes - reservation
	if otherReservations < 0 || size > s.config.MaxStorageBytes || s.usedBytesLocked() > s.config.MaxStorageBytes-otherReservations-size {
		return record{}, false, errStorageFull
	}
	objectKey := id
	if s.config.Driver == "oss" {
		objectKey = ossObjectPrefix + id
	}
	created := record{ID: id, ObjectKey: objectKey, ContentType: contentType, Size: size, Created: time.Now().UTC()}
	if err := s.store.Put(ctx, created.ObjectKey, sourcePath, contentType); err != nil {
		return record{}, false, err
	}
	s.records[id] = created
	if err := s.saveIndexLocked(); err != nil {
		delete(s.records, id)
		if deleteErr := s.store.Delete(ctx, created.ObjectKey); deleteErr != nil {
			log.Printf("rollback object %s: %v", id, deleteErr)
			s.orphans[created.ObjectKey] = created.Size
		}
		return record{}, false, err
	}
	delete(s.orphans, created.ObjectKey)
	return created, false, nil
}

// reserve earmarks capacity for an in-flight upload so concurrent uploads
// cannot collectively break the storage limit. It must be balanced by release.
func (s *service) reserve(ctx context.Context, bytes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.cleanupLocked(ctx, time.Now().Add(-s.config.Retention)); err != nil {
		return err
	}
	if bytes <= 0 || bytes > s.config.MaxStorageBytes || s.usedBytesLocked() > s.config.MaxStorageBytes-s.reservedBytes-bytes {
		return errStorageFull
	}
	s.reservedBytes += bytes
	return nil
}

// release returns previously reserved capacity. Safe to call with a partial or
// zero reservation (e.g. when the actual size was unknown).
func (s *service) release(bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reservedBytes -= bytes
	if s.reservedBytes < 0 {
		s.reservedBytes = 0
	}
}

// restoreReservation rebuilds in-flight tus upload reservations found on disk
// during startup. They are intentionally allowed to exceed a newly lowered
// limit so no further upload can make the overage worse.
func (s *service) restoreReservation(bytes int64) {
	if bytes <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reservedBytes <= (1<<63-1)-bytes {
		s.reservedBytes += bytes
	} else {
		s.reservedBytes = 1<<63 - 1
	}
}

// usedBytesLocked returns the total bytes occupied by committed objects plus
// known orphans and current reservations. Caller must hold s.mu.
func (s *service) usedBytesLocked() int64 {
	var used int64
	for _, item := range s.records {
		if item.Size > 0 && used <= (1<<63-1)-item.Size {
			used += item.Size
		}
	}
	for _, size := range s.orphans {
		if size > 0 && used <= (1<<63-1)-size {
			used += size
		}
	}
	return used
}
