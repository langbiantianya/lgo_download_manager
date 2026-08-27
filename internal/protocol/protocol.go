// Package protocol defines the unified interface every transport driver
// implements plus the capability/option structs shared by them. Concrete
// drivers live in protocol_*.go files in the same package.
//
// The interface intentionally mirrors the design doc: Probe for resource
// discovery, DownloadChunk for multi-threaded range fetches, and
// DownloadFallback for single-stream servers without Range support.
package protocol

import (
	"context"
	"os"
)

// DriverCapabilities is the result of a successful Probe — enough metadata
// for the engine to plan how it will download the file.
type DriverCapabilities struct {
	TotalSize    int64  // total byte length; -1 if unknown
	SupportRange bool   // true if the server honors Range / FTP REST
	ServerInfo   string // free-form banner / server header
}

// AuthOptions captures credentials and request-decoration knobs a driver
// can honor. Fields are protocol-agnostic; drivers ignore what they
// don't understand.
type AuthOptions struct {
	Username   string
	Password   string
	UserAgent  string
	Cookies    string
	Referer    string
	FTPPassive bool // FTP only
}

// ProtocolDriver is what every transport implements. All methods MUST honor
// the context's cancellation/deadline.
type ProtocolDriver interface {
	// Probe inspects the resource and reports its capabilities. For
	// authenticated URLs this is the right place to handshake.
	Probe(ctx context.Context) (*DriverCapabilities, error)

	// DownloadChunk fetches bytes in [start, end] (inclusive) and writes
	// them at offset `start` in `file`. The onData callback is called
	// after every successful read with the number of bytes just appended.
	// Implementations should also verify the count and ensure ordering.
	DownloadChunk(ctx context.Context, start, end int64, file *os.File, onData func(n int)) error

	// DownloadFallback streams the resource from `offset` and appends to
	// the file (no range support on the server). Used as a fallback for
	// the multi-threaded path.
	DownloadFallback(ctx context.Context, offset int64, file *os.File, onData func(n int)) error

	// Close releases any pooled connections and resources.
	Close() error
}
