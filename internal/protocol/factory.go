package protocol

import (
	"fmt"
	"net/url"
	"strings"
)

// ProtocolKind is an enum-friendly string for the supported transports.
type ProtocolKind string

const (
	ProtoHTTP   ProtocolKind = "HTTP"
	ProtoHTTPS  ProtocolKind = "HTTPS"
	ProtoFTP    ProtocolKind = "FTP"
	ProtoWebDAV ProtocolKind = "WEBDAV"
)

// DetectKind resolves the protocol to use for a URL. Mapping rules follow
// the design doc:
//
//   - http://, https://   -> HTTP / HTTPS  (webdav:// is rewritten to https
//     for compatibility with some NAS firmwares; we keep a separate code path)
//   - ftp://               -> FTP
//   - webdav://, dav://    -> WEBDAV (mapped onto HTTP underneath)
//
// An explicit hint from the caller (e.g. a UI selection) wins.
func DetectKind(raw string, hint ProtocolKind) (ProtocolKind, error) {
	if hint != "" {
		return hint, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return ProtoHTTP, nil
	case "https":
		return ProtoHTTPS, nil
	case "ftp":
		return ProtoFTP, nil
	case "webdav", "dav":
		return ProtoWebDAV, nil
	default:
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
}

// ResolveURL returns a usable transport URL. For WEBDAV the caller passes
// the original webdav:// scheme; the webdav driver rewrites it to http(s).
func ResolveURL(raw string, kind ProtocolKind) string {
	if kind == ProtoWebDAV {
		if strings.HasPrefix(raw, "webdav://") {
			return "https://" + strings.TrimPrefix(raw, "webdav://")
		}
		if strings.HasPrefix(raw, "dav://") {
			return "http://" + strings.TrimPrefix(raw, "dav://")
		}
	}
	return raw
}

// Auth holds the credentials carried alongside a task. Drivers read what
// they need; irrelevant fields are ignored.
type Auth struct {
	AuthOptions
}

// New returns the right driver for `kind`. All drivers validate the URL.
// The returned driver owns its own resources; Close() must be called.
func New(raw string, kind ProtocolKind, auth Auth) (ProtocolDriver, error) {
	url := ResolveURL(raw, kind)
	switch kind {
	case ProtoHTTP, ProtoHTTPS:
		return newHTTPDriver(url, auth.AuthOptions)
	case ProtoFTP:
		return newFTPDriver(url, auth.AuthOptions)
	case ProtoWebDAV:
		return newWebDAVDriver(url, auth.AuthOptions)
	default:
		return nil, fmt.Errorf("protocol: no driver for kind=%s", kind)
	}
}
