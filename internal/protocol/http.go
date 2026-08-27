package protocol

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// chunkSize is the read buffer used when streaming body bytes to disk.
// 256 KiB is a sweet spot for sequential throughput without excessive
// syscall overhead.
const chunkSize = 256 * 1024

// httpDriver implements ProtocolDriver for HTTP and HTTPS. It keeps one
// per-driver http.Client because each chunk goroutine will use its own
// short-lived client (round-trippers are reused via Transport).
type httpDriver struct {
	url  string
	auth AuthOptions
	cli  *http.Client
}

// newHTTPDriver is the factory entry point called by New(...).
func newHTTPDriver(raw string, auth AuthOptions) (ProtocolDriver, error) {
	if raw == "" {
		return nil, fmt.Errorf("http: empty url")
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &httpDriver{
		url:  raw,
		auth: auth,
		cli:  &http.Client{Transport: tr, Timeout: 0}, // no overall timeout; engine uses ctx
	}, nil
}

// decorate applies user options to a new request: User-Agent, Cookies,
// Referer, and (optionally) Basic Auth. Basic Auth only fires when both
// username and password are non-empty — we don't want to send a half-set
// Authorization header.
func (d *httpDriver) decorate(req *http.Request) {
	if d.auth.UserAgent != "" {
		req.Header.Set("User-Agent", d.auth.UserAgent)
	}
	if d.auth.Cookies != "" {
		req.Header.Set("Cookie", d.auth.Cookies)
	}
	if d.auth.Referer != "" {
		req.Header.Set("Referer", d.auth.Referer)
	}
	if d.auth.Username != "" || d.auth.Password != "" {
		req.SetBasicAuth(d.auth.Username, d.auth.Password)
	}
}

// Probe issues a HEAD (falling back to a 1-byte Range GET) to discover the
// file's size and whether the server supports byte ranges.
func (d *httpDriver) Probe(ctx context.Context) (*DriverCapabilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, d.url, nil)
	if err != nil {
		return nil, fmt.Errorf("http probe build: %w", err)
	}
	d.decorate(req)

	resp, err := d.cli.Do(req)
	if err != nil {
		// HEAD sometimes blocked; try a tiny ranged GET.
		return d.probeWithRange(ctx)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http probe: status %d", resp.StatusCode)
	}

	caps := &DriverCapabilities{
		ServerInfo:   strings.Join(resp.Header["Server"], ", "),
		SupportRange: supportsRanges(resp),
		TotalSize:    parseInt64(resp.Header.Get("Content-Length")),
	}
	// Some servers omit Content-Length on HEAD but include it on GET.
	if caps.TotalSize < 0 {
		return d.probeWithRange(ctx)
	}
	return caps, nil
}

// probeWithRange does a 1-byte ranged GET to coerce the server into
// revealing both Content-Length and Accept-Ranges.
func (d *httpDriver) probeWithRange(ctx context.Context) (*DriverCapabilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return nil, fmt.Errorf("http probe range build: %w", err)
	}
	d.decorate(req)
	req.Header.Set("Range", "bytes=0-0")

	resp, err := d.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http probe range do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http probe range: status %d", resp.StatusCode)
	}
	caps := &DriverCapabilities{
		SupportRange: resp.StatusCode == http.StatusPartialContent,
		ServerInfo:   strings.Join(resp.Header["Server"], ", "),
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		caps.TotalSize = parseTotalFromContentRange(cr)
	} else {
		caps.TotalSize = parseInt64(resp.Header.Get("Content-Length"))
	}
	// Drain the 1 byte we asked for; we don't need it.
	_, _ = io.Copy(io.Discard, resp.Body)
	return caps, nil
}

// supportsRanges returns true if the server advertises range support.
// "bytes" is the canonical value; some servers use "1" / lack the header.
func supportsRanges(resp *http.Response) bool {
	v := strings.ToLower(resp.Header.Get("Accept-Ranges"))
	if v == "" {
		return false
	}
	return v == "bytes" || v == "1"
}

// parseInt64 parses a possibly-empty header value to int64, returning -1
// when missing or invalid.
func parseInt64(s string) int64 {
	if s == "" {
		return -1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// parseTotalFromContentRange parses "bytes 0-0/12345" -> 12345.
func parseTotalFromContentRange(s string) int64 {
	idx := strings.LastIndex(s, "/")
	if idx < 0 {
		return -1
	}
	return parseInt64(strings.TrimSpace(s[idx+1:]))
}

// DownloadChunk performs an HTTP GET with "Range: bytes=start-end" and
// writes the body to file at `start`. onData is called per Read with the
// number of bytes just appended (useful for live progress).
func (d *httpDriver) DownloadChunk(ctx context.Context, start, end int64, file *os.File, onData func(n int)) error {
	if start < 0 || end < start {
		return fmt.Errorf("http chunk: invalid range [%d,%d]", start, end)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return fmt.Errorf("http chunk build: %w", err)
	}
	d.decorate(req)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("http chunk do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("http chunk: status %d", resp.StatusCode)
	}

	buf := make([]byte, chunkSize)
	off := start
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("http chunk write: %w", werr)
			}
			off += int64(n)
			if onData != nil {
				onData(n)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("http chunk read: %w", rerr)
		}
		if off > end+1 {
			// Defensive: some servers over-shoot the requested end. Stop
			// writing once we've reached the boundary.
			break
		}
	}
	return nil
}

// DownloadFallback is the single-stream fallback for servers without
// Range support. It writes starting at `offset`.
func (d *httpDriver) DownloadFallback(ctx context.Context, offset int64, file *os.File, onData func(n int)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return fmt.Errorf("http fallback build: %w", err)
	}
	d.decorate(req)

	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("http fallback do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("http fallback: status %d", resp.StatusCode)
	}

	buf := make([]byte, chunkSize)
	off := offset
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("http fallback write: %w", werr)
			}
			off += int64(n)
			if onData != nil {
				onData(n)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("http fallback read: %w", rerr)
		}
	}
	return nil
}

// Close shuts down the underlying http.Client's idle connections. Safe to
// call repeatedly.
func (d *httpDriver) Close() error {
	if tr, ok := d.cli.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	return nil
}
