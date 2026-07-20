package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// record is the persisted metadata for one stored object.
type record struct {
	ID          string    `json:"id"`
	ObjectKey   string    `json:"object_key"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Created     time.Time `json:"created"`
}

// service owns the in-memory object index, the storage backend, and the
// capacity reservation ledger. All mutations of records/reservedBytes happen
// under mu; long-running storage operations are performed outside the lock.
type service struct {
	config        config
	store         blobStore
	policy        typePolicy
	auth          *auth
	tus           http.Handler
	tusDir        string
	mu            sync.RWMutex
	records       map[string]record
	tusResults    map[string]record
	reservedBytes int64
	orphans       map[string]int64
}

func newService(cfg config) (*service, error) {
	for _, directory := range []string{cfg.DataDir, filepath.Join(cfg.DataDir, "tmp"), filepath.Join(cfg.DataDir, "objects")} {
		if err := os.MkdirAll(directory, 0750); err != nil {
			return nil, err
		}
	}
	policy, err := parseTypePolicy(cfg.AllowedTypes)
	if err != nil {
		return nil, err
	}
	auth, err := newAuth(cfg.AuthPassword)
	if err != nil {
		return nil, err
	}
	service := &service{config: cfg, policy: policy, auth: auth, records: map[string]record{}, tusResults: map[string]record{}, orphans: map[string]int64{}}
	if cfg.Driver == "oss" {
		client, err := oss.New(cfg.OSSEndpoint, cfg.OSSAccessKeyID, cfg.OSSAccessKey)
		if err != nil {
			return nil, fmt.Errorf("create OSS client: %w", err)
		}
		bucket, err := client.Bucket(cfg.OSSBucket)
		if err != nil {
			return nil, fmt.Errorf("open OSS bucket: %w", err)
		}
		service.store = ossStore{bucket: bucket}
	} else {
		service.store = localStore{root: filepath.Join(cfg.DataDir, "objects")}
	}
	if err := service.loadIndex(); err != nil {
		return nil, err
	}
	if err := service.reconcile(context.Background()); err != nil {
		return nil, err
	}
	if err := service.initTus(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *service) loadIndex() error {
	contents, err := os.ReadFile(filepath.Join(s.config.DataDir, "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(contents, &s.records); err != nil {
		return fmt.Errorf("read index.json: %w", err)
	}
	return nil
}

func (s *service) saveIndexLocked() error {
	contents, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(s.config.DataDir, "index-*.json")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(contents); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, filepath.Join(s.config.DataDir, "index.json"))
}

// urlFor builds the public retrieval URL for a record. For OSS it uses the
// bucket public base URL; for local storage it prefers PUBLIC_BASE_URL and
// falls back to the request host so LAN access keeps working without config.
func (s *service) urlFor(r *http.Request, item record) string {
	if s.config.Driver == "oss" {
		return s.config.OSSPublicURL + "/" + item.ObjectKey
	}
	base := s.config.PublicBaseURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/files/" + item.ID + "?key=" + s.auth.fileKey(item.ID)
}
