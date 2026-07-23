package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// ossObjectPrefix namespaces objects inside the OSS bucket.
const ossObjectPrefix = "image-host/"

// sha256Pattern matches a lowercase 64-character hex SHA-256 digest. Used to
// validate object keys and route parameters before touching the filesystem.
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// blobStore is the storage abstraction used by both the local disk and the
// Alibaba Cloud OSS backends. Put is expected to be idempotent for an existing
// key (deduplication is handled at the service layer).
type blobStore interface {
	Put(ctx context.Context, key, sourcePath, contentType string) error
	Delete(ctx context.Context, key string) error
	SignURL(key string, expires time.Duration) (string, error)
	// Open returns a reader for the stored object's raw bytes. It is used by
	// the file manager to stream objects into a zip archive for batch download.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// localStore keeps objects as flat files named by their SHA-256 key under root.
type localStore struct{ root string }

func (s localStore) path(key string) (string, error) {
	if !sha256Pattern.MatchString(key) {
		return "", errors.New("invalid object key")
	}
	return filepath.Join(s.root, key), nil
}

func (s localStore) Put(_ context.Context, key, sourcePath, _ string) error {
	destination, err := s.path(key)
	if err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		return os.Remove(sourcePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(sourcePath, destination)
}

func (s localStore) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s localStore) SignURL(_ string, _ time.Duration) (string, error) {
	return "", errors.New("local storage does not use signed URLs")
}

// Open opens the stored object for reading. The caller must close the reader.
func (s localStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

// ossStore uploads objects to an Alibaba Cloud OSS bucket. Deduplication and
// expiration are still managed by the service via the object index.
type ossStore struct{ bucket *oss.Bucket }

func (s ossStore) Put(_ context.Context, key, sourcePath, contentType string) error {
	return s.bucket.PutObjectFromFile(key, sourcePath, oss.ContentType(contentType))
}

func (s ossStore) Delete(_ context.Context, key string) error { return s.bucket.DeleteObject(key) }

func (s ossStore) SignURL(key string, expires time.Duration) (string, error) {
	return s.bucket.SignURL(key, oss.HTTPGet, int64(expires.Seconds()))
}

// Open downloads the object from OSS into a reader. The caller must close it.
func (s ossStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.bucket.GetObject(key, oss.WithContext(ctx))
}
