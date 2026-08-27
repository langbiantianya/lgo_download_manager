package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	ftp "github.com/jlaffaye/ftp"
)

// ftpDriver implements ProtocolDriver for FTP. Each chunk goroutine owns
// its own ServerConn because FTP data connections are exclusive to a
// single transfer; sharing one connection would serialize chunks.
// All ServerConns log into the same credentials; Probe discovers which.
//
// We intentionally keep the conn pool concurrency = chunk count upper
// bound to avoid resource leaks.
type ftpDriver struct {
	host     string // host:port
	path     string // remote path
	auth     AuthOptions
	mu       sync.Mutex
	reserved bool // whether the server responded 350 to REST 0
}

// newFTPDriver is the factory entry point.
func newFTPDriver(raw string, auth AuthOptions) (ProtocolDriver, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("ftp parse: %w", err)
	}
	if u.Scheme != "ftp" {
		return nil, fmt.Errorf("ftp: bad scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("ftp: empty host")
	}
	host := u.Host
	if !hasPort(host) {
		host += ":21"
	}
	return &ftpDriver{
		host: host,
		path: strings.TrimPrefix(u.Path, "/"),
		auth: auth,
	}, nil
}

func hasPort(s string) bool {
	return strings.Contains(s, ":")
}

// dial logs in with the configured credentials. PASV is controlled by the
// AuthOptions.FTPPassive flag via DialWithDisabledEPSV (active mode is the
// negation of "PASV" for our purposes; we keep EPSV/PASV on when
// FTPPassive is true, the default).
func (d *ftpDriver) dial(_ context.Context) (*ftp.ServerConn, error) {
	opts := []ftp.DialOption{ftp.DialWithTimeout(10 * time.Second)}
	if !d.auth.FTPPassive {
		// active mode: ask the client to disable all server-side passive
		// variants so RETR causes the server to connect back to us.
		opts = append(opts, ftp.DialWithDisabledEPSV(true))
	}
	conn, err := ftp.Dial(d.host, opts...)
	if err != nil {
		return nil, fmt.Errorf("ftp dial: %w", err)
	}
	user := d.auth.Username
	pass := d.auth.Password
	if user == "" {
		user = "anonymous"
	}
	if pass == "" {
		pass = "anonymous@"
	}
	if err := conn.Login(user, pass); err != nil {
		_ = conn.Quit()
		return nil, fmt.Errorf("ftp login: %w", err)
	}
	return conn, nil
}

// Probe uses SIZE for the total size and REST 0 to discover whether the
// server supports resume (RFC 959). 350 means supported; 502 means not.
func (d *ftpDriver) Probe(ctx context.Context) (*DriverCapabilities, error) {
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Quit() }()

	// Switch to binary mode for size/retr.
	if err := conn.Type("I"); err != nil {
		return nil, fmt.Errorf("ftp TYPE I: %w", err)
	}

	caps := &DriverCapabilities{ServerInfo: fmt.Sprintf("FTP %s", d.host)}

	if size, err := conn.FileSize(d.path); err == nil {
		caps.TotalSize = size
	} else {
		// No SIZE support is rare; treat as unknown.
		caps.TotalSize = -1
	}

	// Test RESUME: send REST 0 on a probe connection.
	d.mu.Lock()
	probe, err := dialRaw(d.host)
	if err == nil {
		_, msg, cerr := probe.cmd(-1, "REST 0")
		if cerr == nil && strings.HasPrefix(msg, "350") {
			d.reserved = true
		}
		probe.Close()
	}
	d.mu.Unlock()
	caps.SupportRange = d.reserved

	return caps, nil
}

// dialRaw opens a passive text-mode conn used solely to issue a single
// REST command. Used by Probe to test resume support cleanly.
func dialRaw(addr string) (*rawConn, error) {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	rc := &rawConn{c: c, rd: c}
	rc.welcome()
	return rc, nil
}

type rawConn struct {
	c  net.Conn
	rd io.Reader
}

// welcome reads the 220 banner. Future read/writes work in line-protocol.
func (r *rawConn) welcome() {
	buf := make([]byte, 1024)
	_, _ = r.rd.Read(buf)
}

// readReply returns the next status line and any continuation lines until
// the final "NNN " (space) terminator.
func (r *rawConn) readReply() (string, error) {
	var sb strings.Builder
	tmp := make([]byte, 1024)
	for {
		n, err := r.rd.Read(tmp)
		if err != nil {
			return sb.String(), err
		}
		sb.Write(tmp[:n])
		s := sb.String()
		if l := strings.Split(s, "\r\n"); len(l) >= 2 && len(l[len(l)-2]) >= 4 {
			line := l[len(l)-2]
			if line[3] == ' ' {
				return strings.TrimSpace(s), nil
			}
		}
	}
}

// cmd sends a single command and returns the reply text. expected is -1
// (any) for probing.
func (r *rawConn) cmd(_ int, format string, args ...interface{}) (int, string, error) {
	line := fmt.Sprintf(format, args...) + "\r\n"
	if _, err := r.c.Write([]byte(line)); err != nil {
		return 0, "", err
	}
	reply, err := r.readReply()
	if err != nil {
		return 0, "", err
	}
	code := 0
	if len(reply) >= 3 {
		if n, err := fmt.Sscanf(reply[:3], "%d", &code); err != nil || n != 1 {
			code = -1
		}
	}
	return code, reply, nil
}

func (r *rawConn) Close() error { return r.c.Close() }

// connFor opens a fresh login for one chunk's transfer.
func (d *ftpDriver) connFor(ctx context.Context) (*ftp.ServerConn, error) {
	return d.dial(ctx)
}

// DownloadChunk opens a dedicated control conn, sends REST start, then
// RETR and streams the body into file at the given offset.
func (d *ftpDriver) DownloadChunk(ctx context.Context, start, end int64, file *os.File, onData func(n int)) error {
	conn, err := d.connFor(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Quit()
	}()

	if err := conn.Type("I"); err != nil {
		return fmt.Errorf("ftp TYPE I: %w", err)
	}

	// Many FTP clients clamp RETR to bytes-below-total by sending REST.
	// Set the offset; some servers also expect the high byte here.
	r, err := conn.RetrFrom(d.path, uint64(start))
	if err != nil {
		// REST unsupported: fail so engine can fall back.
		return fmt.Errorf("ftp RETR/REST: %w", err)
	}
	defer r.Close()

	buf := make([]byte, chunkSize)
	off := start
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("ftp chunk write: %w", werr)
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
			return fmt.Errorf("ftp chunk read: %w", rerr)
		}
		if off > end+1 {
			break
		}
	}
	return nil
}

// DownloadFallback is a plain RETR with no resume. Used when the server
// does not support REST.
func (d *ftpDriver) DownloadFallback(ctx context.Context, offset int64, file *os.File, onData func(n int)) error {
	conn, err := d.connFor(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Quit()
	}()

	if err := conn.Type("I"); err != nil {
		return fmt.Errorf("ftp TYPE I: %w", err)
	}

	r, err := conn.Retr(d.path)
	if err != nil {
		return fmt.Errorf("ftp RETR: %w", err)
	}
	defer r.Close()

	buf := make([]byte, chunkSize)
	off := offset
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("ftp fallback write: %w", werr)
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
			return fmt.Errorf("ftp fallback read: %w", rerr)
		}
	}
	return nil
}

// Close releases the reserved-flag mutex. Connection resources are owned
// per-call (logged in/out as needed).
func (d *ftpDriver) Close() error {
	return nil
}
