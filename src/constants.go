//go:build ignore

package main

// Hard limits shared by single-shot and chunked uploads.
const (
	defaultMaxUploadBytes int64 = 10 * 1024 * 1024 * 1024
	maxMultipartOverhead  int64 = 1024 * 1024
	ossObjectPrefix             = "image-host/"

	// maxChunkSize bounds a single chunk payload. 32 MiB keeps per-request
	// memory bounded while giving XHR upload-progress events enough granularity.
	maxChunkSize int64 = 32 * 1024 * 1024

	// defaultChunkSize is suggested to clients when they do not pick one.
	defaultChunkSize int64 = 8 * 1024 * 1024
)

// chunkSize returns the chunk size to advertise to clients. It clamps the
// per-upload maximum to a safe default derived from the configured single-file
// cap, so very large files still produce a bounded number of chunks.
func (c config) chunkSize() int64 {
	size := defaultChunkSize
	// Keep at most ~1024 chunks per file for sane progress reporting.
	if cap := c.MaxUploadBytes / 1024; cap > 0 && cap < size {
		size = cap
	}
	if size < 1<<20 {
		size = 1 << 20
	}
	if size > maxChunkSize {
		size = maxChunkSize
	}
	return size
}
