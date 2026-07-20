package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

const (
	defaultMaxUploadBytes int64 = 10 * 1024 * 1024 * 1024
	maxMultipartOverhead  int64 = 1024 * 1024
	ossObjectPrefix             = "image-host/"
)

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type config struct {
	ListenAddr      string
	DataDir         string
	PublicBaseURL   string
	MaxUploadBytes  int64
	MaxStorageBytes int64
	Retention       time.Duration
	Driver          string
	AllowedTypes    string
	OSSEndpoint     string
	OSSBucket       string
	OSSAccessKeyID  string
	OSSAccessKey    string
	OSSPublicURL    string
}

type record struct {
	ID          string    `json:"id"`
	ObjectKey   string    `json:"object_key"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Created     time.Time `json:"created"`
}

type blobStore interface {
	Put(ctx context.Context, key, sourcePath, contentType string) error
	Delete(ctx context.Context, key string) error
}

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

type ossStore struct{ bucket *oss.Bucket }

func (s ossStore) Put(_ context.Context, key, sourcePath, contentType string) error {
	return s.bucket.PutObjectFromFile(key, sourcePath, oss.ContentType(contentType))
}

func (s ossStore) Delete(_ context.Context, key string) error { return s.bucket.DeleteObject(key) }

type service struct {
	config        config
	store         blobStore
	policy        typePolicy
	mu            sync.RWMutex
	records       map[string]record
	reservedBytes int64
	orphans       map[string]int64
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	service, err := newService(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := service.cleanup(context.Background()); err != nil {
		log.Printf("initial cleanup failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/upload", service.upload)
	mux.HandleFunc("GET /files/{id}", service.file)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           logging(mux),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
		ReadTimeout:       2 * time.Hour,
	}
	go service.cleanupLoop(context.Background())
	log.Printf("easy-temp-host listening on %s with %s storage (allowed types: %s)", cfg.ListenAddr, cfg.Driver, service.policy)
	log.Fatal(server.ListenAndServe())
}

func loadConfig() (config, error) {
	cfg := config{
		ListenAddr:      env("LISTEN_ADDR", ":8080"),
		DataDir:         env("DATA_DIR", "/data"),
		PublicBaseURL:   strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		MaxUploadBytes:  defaultMaxUploadBytes,
		MaxStorageBytes: defaultMaxUploadBytes,
		Retention:       24 * time.Hour,
		Driver:          strings.ToLower(env("STORAGE_DRIVER", "local")),
		AllowedTypes:    strings.TrimSpace(os.Getenv("ALLOWED_TYPES")),
		OSSEndpoint:     os.Getenv("OSS_ENDPOINT"),
		OSSBucket:       os.Getenv("OSS_BUCKET"),
		OSSAccessKeyID:  os.Getenv("OSS_ACCESS_KEY_ID"),
		OSSAccessKey:    os.Getenv("OSS_ACCESS_KEY_SECRET"),
		OSSPublicURL:    strings.TrimRight(os.Getenv("OSS_PUBLIC_BASE_URL"), "/"),
	}
	if raw := os.Getenv("MAX_UPLOAD_BYTES"); raw != "" {
		value, err := parseBytes(raw)
		if err != nil || value <= 0 || value > defaultMaxUploadBytes {
			return config{}, fmt.Errorf("invalid MAX_UPLOAD_BYTES %q", raw)
		}
		cfg.MaxUploadBytes = value
	}
	if raw := os.Getenv("MAX_STORAGE_BYTES"); raw != "" {
		value, err := parseBytes(raw)
		if err != nil || value <= 0 || value > defaultMaxUploadBytes {
			return config{}, fmt.Errorf("invalid MAX_STORAGE_BYTES %q", raw)
		}
		cfg.MaxStorageBytes = value
	}
	if raw := os.Getenv("RETENTION_HOURS"); raw != "" {
		hours, err := strconv.Atoi(raw)
		if err != nil || hours <= 0 {
			return config{}, fmt.Errorf("invalid RETENTION_HOURS %q", raw)
		}
		cfg.Retention = time.Duration(hours) * time.Hour
	}
	if cfg.Driver != "local" && cfg.Driver != "oss" {
		return config{}, fmt.Errorf("STORAGE_DRIVER must be local or oss")
	}
	if cfg.Driver == "oss" {
		if cfg.OSSEndpoint == "" || cfg.OSSBucket == "" || cfg.OSSAccessKeyID == "" || cfg.OSSAccessKey == "" || cfg.OSSPublicURL == "" {
			return config{}, errors.New("OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, OSS_ACCESS_KEY_SECRET, and OSS_PUBLIC_BASE_URL are required for oss storage")
		}
	}
	return cfg, nil
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
	service := &service{config: cfg, policy: policy, records: map[string]record{}, orphans: map[string]int64{}}
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

// reconcile removes files left behind if the process stopped between writing an
// object and committing its metadata. It only runs before accepting requests.
func (s *service) reconcile(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tempEntries, err := os.ReadDir(filepath.Join(s.config.DataDir, "tmp"))
	if err != nil {
		return err
	}
	for _, entry := range tempEntries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "upload-") {
			if err := os.Remove(filepath.Join(s.config.DataDir, "tmp", entry.Name())); err != nil {
				return err
			}
		}
	}

	changed := false
	switch store := s.store.(type) {
	case localStore:
		entries, err := os.ReadDir(store.root)
		if err != nil {
			return err
		}
		for id, item := range s.records {
			path, err := store.path(item.ObjectKey)
			if err != nil || !fileExists(path) {
				delete(s.records, id)
				changed = true
			}
		}
		for _, entry := range entries {
			if entry.IsDir() || !sha256Pattern.MatchString(entry.Name()) {
				continue
			}
			if _, indexed := s.records[entry.Name()]; !indexed {
				if err := os.Remove(filepath.Join(store.root, entry.Name())); err != nil {
					return err
				}
			}
		}
	case ossStore:
		objects := make(map[string]struct{})
		marker := ""
		for {
			result, err := store.bucket.ListObjects(oss.Prefix(ossObjectPrefix), oss.Marker(marker))
			if err != nil {
				return fmt.Errorf("list OSS objects: %w", err)
			}
			for _, object := range result.Objects {
				objects[object.Key] = struct{}{}
			}
			if !result.IsTruncated {
				break
			}
			marker = result.NextMarker
		}
		indexedObjects := make(map[string]struct{}, len(s.records))
		for _, item := range s.records {
			indexedObjects[item.ObjectKey] = struct{}{}
		}
		for id, item := range s.records {
			_, exists := objects[item.ObjectKey]
			if !exists && !strings.HasPrefix(item.ObjectKey, ossObjectPrefix) {
				var err error
				exists, err = store.bucket.IsObjectExist(item.ObjectKey)
				if err != nil {
					return fmt.Errorf("check OSS object %s: %w", item.ObjectKey, err)
				}
			}
			if !exists {
				delete(s.records, id)
				changed = true
			}
		}
		for key := range objects {
			if _, indexed := indexedObjects[key]; !indexed {
				if err := store.Delete(context.Background(), key); err != nil {
					return fmt.Errorf("delete orphaned OSS object %s: %w", key, err)
				}
			}
		}
	}
	if changed {
		return s.saveIndexLocked()
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (s *service) upload(w http.ResponseWriter, r *http.Request) {
	maxRequestBytes := s.config.MaxUploadBytes + maxMultipartOverhead
	if r.ContentLength > maxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds maximum upload size")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	reservation := s.config.MaxUploadBytes
	if r.ContentLength >= 0 && r.ContentLength < reservation {
		reservation = r.ContentLength
	}
	if err := s.reserve(r.Context(), reservation); err != nil {
		if errors.Is(err, errStorageFull) {
			writeError(w, http.StatusInsufficientStorage, "storage capacity exceeded")
		} else {
			log.Printf("reserve upload capacity: %v", err)
			writeError(w, http.StatusInternalServerError, "could not prepare upload")
		}
		return
	}
	defer s.release(reservation)
	filePath, contentType, size, id, err := s.readUpload(r)
	if err != nil {
		if errors.As(err, new(*http.MaxBytesError)) || errors.Is(err, errTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds maximum upload size")
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	defer os.Remove(filePath)

	created, duplicate, err := s.persist(r.Context(), id, filePath, contentType, size, reservation)
	if err != nil {
		log.Printf("upload %s: %v", id, err)
		if errors.Is(err, errStorageFull) {
			writeError(w, http.StatusInsufficientStorage, "storage capacity exceeded")
		} else {
			writeError(w, http.StatusInternalServerError, "could not store upload")
		}
		return
	}
	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"url":     s.urlFor(r, created),
		"created": created.Created.UTC().Format(time.RFC3339),
	})
}

var (
	errTooLarge    = errors.New("file too large")
	errStorageFull = errors.New("storage capacity exceeded")
)

func (s *service) readUpload(r *http.Request) (string, string, int64, string, error) {
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return "", "", 0, "", errors.New("Content-Type must be multipart/form-data")
	}
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", 0, "", fmt.Errorf("read multipart body: %w", err)
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		return s.writePart(part)
	}
	return "", "", 0, "", errors.New("multipart field file is required")
}

func (s *service) writePart(part *multipart.Part) (string, string, int64, string, error) {
	temp, err := os.CreateTemp(filepath.Join(s.config.DataDir, "tmp"), "upload-*")
	if err != nil {
		return "", "", 0, "", err
	}
	path := temp.Name()
	cleanup := func(err error) (string, string, int64, string, error) {
		temp.Close()
		os.Remove(path)
		return "", "", 0, "", err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(part, s.config.MaxUploadBytes+1))
	if err != nil {
		return cleanup(err)
	}
	if written == 0 {
		return cleanup(errors.New("file is empty"))
	}
	if written > s.config.MaxUploadBytes {
		return cleanup(errTooLarge)
	}
	if err := temp.Close(); err != nil {
		os.Remove(path)
		return "", "", 0, "", err
	}
	file, err := os.Open(path)
	if err != nil {
		os.Remove(path)
		return "", "", 0, "", err
	}
	probe := make([]byte, 512)
	read, readErr := file.Read(probe)
	file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		os.Remove(path)
		return "", "", 0, "", readErr
	}
	contentType := normalizeContentType(http.DetectContentType(probe[:read]))
	if !s.policy.allows(contentType) {
		os.Remove(path)
		return "", "", 0, "", fmt.Errorf("content type %q is not allowed (allowed types: %s)", contentType, s.policy)
	}
	return path, contentType, written, hex.EncodeToString(hash.Sum(nil)), nil
}

// typePolicy decides whether a detected MIME type may be stored.
type typePolicy struct {
	allowAll bool
	exact    map[string]struct{}
	prefixes []string
	raw      string
}

// presetGroups expands named aliases to their constituent MIME types.
var presetGroups = map[string][]string{
	"images": {"image/jpeg", "image/png", "image/gif", "image/webp"},
	"videos": {"video/mp4", "video/webm", "video/quicktime", "video/x-matroska", "video/x-msvideo", "video/mpeg"},
	"audio":  {"audio/mpeg", "audio/ogg", "audio/wav", "audio/x-wav", "audio/webm", "audio/aac", "audio/flac"},
	"docs":   {"application/pdf", "text/plain", "text/markdown", "text/html"},
}

// parseTypePolicy builds a typePolicy from the ALLOWED_TYPES env value.
// Empty / "all" => accept anything; "images" => the default image set;
// otherwise a comma-separated list that may mix aliases (images, videos),
// prefix wildcards (image/*, video/*), and exact MIME types.
func parseTypePolicy(raw string) (typePolicy, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		raw = "all"
	}
	policy := typePolicy{exact: map[string]struct{}{}, raw: raw}
	if raw == "all" {
		policy.allowAll = true
		return policy, nil
	}
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.HasSuffix(token, "/*") {
			policy.prefixes = append(policy.prefixes, strings.TrimSuffix(token, "/*"))
			continue
		}
		if group, ok := presetGroups[token]; ok {
			for _, mt := range group {
				policy.exact[mt] = struct{}{}
			}
			continue
		}
		if !validMIME(token) {
			return typePolicy{}, fmt.Errorf("invalid MIME type %q in ALLOWED_TYPES", token)
		}
		policy.exact[token] = struct{}{}
	}
	if len(policy.exact) == 0 && len(policy.prefixes) == 0 {
		return typePolicy{}, fmt.Errorf("ALLOWED_TYPES %q contains no usable types", raw)
	}
	return policy, nil
}

// validMIME checks that token looks like a "type/subtype" MIME type.
func validMIME(token string) bool {
	slash := strings.IndexByte(token, '/')
	if slash <= 0 || slash == len(token)-1 {
		return false
	}
	for _, r := range token[:slash] {
		if !isMIMERune(r, false) {
			return false
		}
	}
	for _, r := range token[slash+1:] {
		if !isMIMERune(r, true) {
			return false
		}
	}
	return true
}

// isMIMERune restricts tokens to the RFC 6838 MIME grammar (lowercased).
func isMIMERune(r rune, subtype bool) bool {
	if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' {
		return true
	}
	return subtype && r == '+'
}

func (p typePolicy) allows(contentType string) bool {
	if p.allowAll {
		return true
	}
	if _, ok := p.exact[contentType]; ok {
		return true
	}
	for _, prefix := range p.prefixes {
		if strings.HasPrefix(contentType, prefix+"/") {
			return true
		}
	}
	return false
}

func (p typePolicy) String() string {
	if p.allowAll {
		return "all"
	}
	parts := make([]string, 0, len(p.prefixes)+len(p.exact))
	for _, prefix := range p.prefixes {
		parts = append(parts, prefix+"/*")
	}
	for mt := range p.exact {
		parts = append(parts, mt)
	}
	if len(parts) == 0 {
		return p.raw
	}
	return strings.Join(parts, ", ")
}

// normalizeContentType lowercases the detected MIME and drops any parameters
// (e.g. "; charset=utf-8") so stored and compared types are canonical.
func normalizeContentType(contentType string) string {
	contentType = strings.TrimSpace(strings.ToLower(contentType))
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = strings.TrimSpace(contentType[:i])
	}
	return contentType
}

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

func (s *service) release(bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reservedBytes -= bytes
	if s.reservedBytes < 0 {
		s.reservedBytes = 0
	}
}

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

func (s *service) file(w http.ResponseWriter, r *http.Request) {
	if s.config.Driver != "local" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id := r.PathValue("id")
	if !sha256Pattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.mu.RLock()
	item, ok := s.records[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	path, err := (localStore{root: filepath.Join(s.config.DataDir, "objects")}).path(item.ObjectKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFile(w, r, path)
}

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

func (s *service) cleanup(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupLocked(ctx, time.Now().Add(-s.config.Retention))
}

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
	return base + "/files/" + item.ID
}

func parseBytes(raw string) (int64, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	multiplier := int64(1)
	for suffix, value := range map[string]int64{"gib": 1024 * 1024 * 1024, "gb": 1000 * 1000 * 1000, "g": 1024 * 1024 * 1024, "mib": 1024 * 1024, "mb": 1000 * 1000, "m": 1024 * 1024} {
		if strings.HasSuffix(raw, suffix) {
			raw = strings.TrimSuffix(raw, suffix)
			multiplier = value
			break
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value > ((1<<63-1)-maxMultipartOverhead)/multiplier {
		return 0, errors.New("invalid byte count")
	}
	return value * multiplier, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
