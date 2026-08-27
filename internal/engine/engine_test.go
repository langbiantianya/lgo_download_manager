package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lgo_download_manager/internal/prealloc"
	"lgo_download_manager/internal/protocol"
)

// TestChunkedDownload spins up an HTTP server serving a randomized file
// and verifies the engine reconstructs it byte-for-byte using 8 chunks.
func TestChunkedDownload(t *testing.T) {
	const size = 4 * 1024 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			start, end, err := parseRange(rng, size)
			if err == nil {
				w.Header().Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(size))
				w.Header().Set("Content-Length", itoa(end-start+1))
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(payload[start : end+1])
				return
			}
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", itoa(size))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatalf("prealloc: %v", err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var progressCalls atomic.Int32
	var lastProg Progress
	job := NewJob(driver, size, dest, Options{
		ChunkCount:    8,
		ProgressEvery: 50 * time.Millisecond,
		Progress: func(p Progress) {
			progressCalls.Add(1)
			lastProg = p
		},
	})

	if err := job.Run(ctx, true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if len(got) != size {
		t.Fatalf("len(got)=%d, want %d", len(got), size)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch")
	}
	if progressCalls.Load() == 0 {
		t.Fatalf("progress never fired")
	}
	if lastProg.DownloadedBytes != size {
		t.Fatalf("last progress DownloadedBytes=%d, want %d", lastProg.DownloadedBytes, size)
	}
}

// TestFallback streams a small file via the fallback path (no range).
func TestFallback(t *testing.T) {
	const size = 256 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			http.Error(w, "no range support", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", itoa(size))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	job := NewJob(driver, -1, dest, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := job.Run(ctx, false); err != nil {
		t.Fatalf("Run fallback: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("mismatch")
	}
}

// parseRange handles "bytes=START-END" with sizes clipped to total-1.
func parseRange(s string, total int64) (int64, int64, error) {
	if !strings.HasPrefix(s, "bytes=") {
		return 0, 0, errBadRange
	}
	body := strings.TrimPrefix(s, "bytes=")
	dash := strings.IndexByte(body, '-')
	if dash < 0 {
		return 0, 0, errBadRange
	}
	start, err := strconv.ParseInt(body[:dash], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.ParseInt(body[dash+1:], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	if end >= total {
		end = total - 1
	}
	return start, end, nil
}

var errBadRange = badRangeErr{}

type badRangeErr struct{}

func (badRangeErr) Error() string { return "bad range" }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
