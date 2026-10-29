// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// chunkSize 是将响应体字节流式写入磁盘时使用的读取缓冲区大小。
// 256 KiB 在顺序吞吐与系统调用开销之间取得较好的折中。
const (
	chunkSize          = 256 * 1024
	maxHTTPConnections = 64
)

// bufPool 复用 chunkSize 大小的读缓冲。每个 DownloadChunk /
// DownloadFallback（以及引擎的每一次重试）都需要一块这样的缓冲，
// 现用现分配会持续制造 256 KiB 的堆垃圾并加重 GC。
// 池中元素类型为 *[]byte，其 len 恒为 chunkSize。
var bufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, chunkSize)
		return &b
	},
}

// getBuf 取出一块长度为 chunkSize 的读缓冲，调用方必须用 putBuf 归还。
func getBuf() *[]byte {
	b, _ := bufPool.Get().(*[]byte)
	return b
}

// putBuf 归还读缓冲。尺寸被改动过的（长度为 0 或不再是 chunkSize）
// 直接丢弃，避免把异常缓冲混进池中。
func putBuf(b *[]byte) {
	if b == nil || len(*b) != chunkSize {
		return
	}
	bufPool.Put(b)
}

// httpBase 承担 httpDriver 与 webdavDriver 共有的部分：URL、鉴权选项、
// 底层 http.Client，以及 decorate / DownloadChunk / DownloadFallback /
// Close。两个驱动只在各自的 Probe 上有差异（HEAD + Range GET 对比
// PROPFIND）。
type httpBase struct {
	kind         string // 错误信息前缀："http" 或 "webdav"
	url          string
	auth         AuthOptions
	cli          *http.Client
	transport    *transportLease
	etag         string
	lastModified string
	probeTimeout time.Duration
}

type transportKey struct {
	proxyMode   ProxyMode
	proxyURL    string
	proxyBypass string
}

type pooledTransport struct {
	tr       *http.Transport
	refs     int
	lastUsed time.Time
}

// transportLease 让每个驱动独占地“持有”一次共享 Transport。Close
// 只归还引用，不关闭仍被其它任务使用的连接池。
type transportLease struct {
	key  transportKey
	once sync.Once
}

const (
	maxCachedTransports    = 2
	defaultProbeRequestTTL = 10 * time.Second
)

var (
	transportPoolMu sync.Mutex
	transportPool   = make(map[transportKey]*pooledTransport)
)

func newHTTPTransport(auth AuthOptions) *http.Transport {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 proxyFunc(auth),
		DialContext:           dialer.DialContext,
		MaxIdleConns:          maxHTTPConnections,
		MaxIdleConnsPerHost:   maxHTTPConnections,
		MaxConnsPerHost:       maxHTTPConnections,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		// 自定义 DialContext 会让 Transport 保守地关闭 HTTP/2 自动协商；
		// 显式开启后可复用同一 HTTPS 连接承载多个分片请求。
		ForceAttemptHTTP2: true,
		// 下载必须保存服务器提供的原始字节，不能让 Transport 自动解压
		// gzip/br 响应，否则 Range 偏移和 Content-Length 都会失真。
		DisableCompression: true,
	}
}

func acquireHTTPTransport(rawAuth AuthOptions) (*http.Transport, *transportLease) {
	auth := effectiveAuth(rawAuth)
	key := transportKey{
		proxyMode:   auth.ProxyMode,
		proxyURL:    strings.TrimSpace(auth.ProxyURL),
		proxyBypass: strings.TrimSpace(auth.ProxyBypass),
	}
	now := time.Now()

	transportPoolMu.Lock()
	defer transportPoolMu.Unlock()
	entry := transportPool[key]
	if entry == nil {
		entry = &pooledTransport{tr: newHTTPTransport(auth)}
		transportPool[key] = entry
	}
	entry.refs++
	entry.lastUsed = now
	evictIdleTransportsLocked()
	return entry.tr, &transportLease{key: key}
}

func releaseHTTPTransport(lease *transportLease) {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		transportPoolMu.Lock()
		defer transportPoolMu.Unlock()
		entry := transportPool[lease.key]
		if entry == nil {
			return
		}
		if entry.refs > 0 {
			entry.refs--
		}
		entry.lastUsed = time.Now()
		evictIdleTransportsLocked()
	})
}

// evictIdleTransportsLocked 最多保留少量常见代理配置。代理设置每次
// 变更都会产生新 key；无界缓存会让旧连接池长期占用 fd 和内存。
func evictIdleTransportsLocked() {
	for len(transportPool) > maxCachedTransports {
		var (
			oldestKey transportKey
			oldest    *pooledTransport
		)
		for key, entry := range transportPool {
			if entry.refs != 0 {
				continue
			}
			if oldest == nil || entry.lastUsed.Before(oldest.lastUsed) {
				oldestKey = key
				oldest = entry
			}
		}
		if oldest == nil {
			return
		}
		delete(transportPool, oldestKey)
		oldest.tr.CloseIdleConnections()
	}
}

// newHTTPBase 构造两个 HTTP 语义驱动共用的客户端。kind 仅用于错误信息前缀。
//
// 这里刻意不设置 http.Client.Timeout——整体时限由引擎传入的 ctx 控制，
// 客户端级超时会在慢速大文件下载中途无差别掐断连接。但 Transport 必须
// 自带拨号与响应头超时：否则服务器接受 TCP 连接却迟迟不发响应头时，
// 任务会永久挂住（ctx 只有在引擎主动取消时才生效）。
func newHTTPBase(kind, raw string, auth AuthOptions) (httpBase, error) {
	if raw == "" {
		return httpBase{}, fmt.Errorf("%s: empty url", kind)
	}
	tr, lease := acquireHTTPTransport(auth)
	return httpBase{
		kind:         kind,
		url:          raw,
		auth:         auth,
		cli:          &http.Client{Transport: tr, Timeout: 0},
		transport:    lease,
		probeTimeout: defaultProbeRequestTTL,
	}, nil
}

// decorate 将用户选项应用到新请求上：User-Agent、Cookies、Referer，
// 以及（可选的）Basic Auth。仅当用户名与密码任一方非空时才会发送
// Basic Auth。
func (d *httpBase) decorate(req *http.Request) {
	// 关闭自动解压还不够：没有显式标识时服务器仍可选择编码后的表示。
	// Range 偏移必须对应磁盘上的原始字节，因此要求 identity。
	req.Header.Set("Accept-Encoding", "identity")
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

// setValidators 记录探测阶段拿到的资源校验器。后续 Range 请求使用
// If-Range，把“文件在下载途中发生了变化”交给服务器原子判断。
func (d *httpBase) setValidators(etag, lastModified string) {
	d.etag = strings.TrimSpace(etag)
	d.lastModified = strings.TrimSpace(lastModified)
}

// ifRangeValue 优先使用强 ETag；弱 ETag 不能用于 If-Range，此时退回
// Last-Modified。Weak/strong 判断遵循 HTTP 的 W/ 前缀约定。
func (d *httpBase) ifRangeValue() string {
	etag := strings.TrimSpace(d.etag)
	if etag != "" && !strings.HasPrefix(strings.ToUpper(etag), "W/") {
		return etag
	}
	return strings.TrimSpace(d.lastModified)
}

func (d *httpBase) applyIfRange(req *http.Request) {
	if validator := d.ifRangeValue(); validator != "" {
		req.Header.Set("If-Range", validator)
	}
}

// responseValidatorsMatch 防守式校验 206 响应携带的版本元数据。
// 部分代理会破坏 If-Range 语义；若响应明确带了不同的 ETag，宁可触发
// 安全的全量重下，也不能把新旧资源的字节拼在一起。
func (d *httpBase) responseValidatorsMatch(resp *http.Response) bool {
	expectedETag := strings.TrimSpace(d.etag)
	responseETag := strings.TrimSpace(resp.Header.Get("ETag"))
	if expectedETag != "" && responseETag != "" {
		return responseETag == expectedETag
	}
	expectedLM := strings.TrimSpace(d.lastModified)
	responseLM := strings.TrimSpace(resp.Header.Get("Last-Modified"))
	if expectedLM != "" && responseLM != "" {
		return responseLM == expectedLM
	}
	return true
}

func captureValidators(caps *DriverCapabilities, header http.Header) {
	caps.ETag = strings.TrimSpace(header.Get("ETag"))
	caps.LastModified = strings.TrimSpace(header.Get("Last-Modified"))
}

// newHTTPStatusError 把响应状态和 Retry-After 一起传给引擎，避免 429/503
// 只能按固定指数退避撞服务器。
func newHTTPStatusError(action string, resp *http.Response) error {
	return NewStatusErrorWithRetryAfter(resp.StatusCode,
		fmt.Errorf("%s: status %d", action, resp.StatusCode),
		parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()))
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		const maxSeconds = int64(24 * time.Hour / time.Second)
		if seconds > maxSeconds {
			seconds = maxSeconds
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

// DownloadChunk 以 "Range: bytes=start-end" 发起 GET，
// 并将响应体从 `start` 偏移处开始写入 file。每次 Read 后，
// 都会以本次追加的字节数调用 onData（便于实时进度展示）。
func (d *httpBase) DownloadChunk(ctx context.Context, start, end int64, file *os.File, onData func(n int)) error {
	if start < 0 || end < start {
		return fmt.Errorf("%s chunk: invalid range [%d,%d]", d.kind, start, end)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return fmt.Errorf("%s chunk build: %w", d.kind, err)
	}
	d.decorate(req)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	d.applyIfRange(req)

	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("%s chunk do: %w", d.kind, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return fmt.Errorf("%s chunk: %w", d.kind, ErrRangeUnavailable)
	}
	if resp.StatusCode != http.StatusPartialContent {
		return newHTTPStatusError(d.kind+" chunk", resp)
	}
	if !d.responseValidatorsMatch(resp) {
		return fmt.Errorf("%s chunk: resource validator changed: %w", d.kind, ErrRangeUnavailable)
	}
	rangeStart, rangeEnd, _, ok := parseContentRange(resp.Header.Get("Content-Range"))
	if !ok || rangeStart != start || rangeEnd != end {
		return fmt.Errorf("%s chunk: invalid Content-Range %q for requested [%d,%d]",
			d.kind, resp.Header.Get("Content-Range"), start, end)
	}
	expected := end - start + 1
	if resp.ContentLength >= 0 && resp.ContentLength != expected {
		return fmt.Errorf("%s chunk: Content-Length %d, want %d", d.kind, resp.ContentLength, expected)
	}

	off := start
	n, err := copyResponseBody(resp.Body, file, off, expected, onData)
	if err != nil {
		return fmt.Errorf("%s chunk read: %w", d.kind, err)
	}
	if n != expected {
		return fmt.Errorf("%s chunk: wrote %d bytes, want %d: %w", d.kind, n, expected, ErrUnexpectedEOF)
	}
	return nil
}

// DownloadFallback 是针对不支持 Range 的服务器的单流回退方案，
// 从 `offset` 偏移处开始写入。
func (d *httpBase) DownloadFallback(ctx context.Context, offset int64, file *os.File, onData func(n int)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return fmt.Errorf("%s fallback build: %w", d.kind, err)
	}
	d.decorate(req)
	if offset > 0 {
		// 无 Range 的流式回退本应从 0 开始。先尝试标准续传；如果服务端
		// 返回 200，调用方必须显式重置，而不能把响应开头写到 offset。
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		d.applyIfRange(req)
	}

	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("%s fallback do: %w", d.kind, err)
	}
	defer resp.Body.Close()
	writeOffset := int64(0)
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if offset == 0 {
			return fmt.Errorf("%s fallback: unexpected partial response", d.kind)
		}
		if !d.responseValidatorsMatch(resp) {
			return fmt.Errorf("%s fallback: resource validator changed: %w", d.kind, ErrResumeUnavailable)
		}
		start, _, _, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset {
			return fmt.Errorf("%s fallback: invalid Content-Range %q for offset %d",
				d.kind, resp.Header.Get("Content-Range"), offset)
		}
		writeOffset = offset
	case http.StatusOK:
		if offset > 0 {
			return fmt.Errorf("%s fallback: %w", d.kind, ErrResumeUnavailable)
		}
	default:
		return newHTTPStatusError(d.kind+" fallback", resp)
	}

	n, err := copyResponseBody(resp.Body, file, writeOffset, -1, onData)
	if err != nil {
		return fmt.Errorf("%s fallback read: %w", d.kind, err)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return fmt.Errorf("%s fallback: wrote %d bytes, want %d: %w", d.kind, n, resp.ContentLength, ErrUnexpectedEOF)
	}
	return nil
}

// copyResponseBody 从 off 开始写响应体；limit < 0 表示不限长度。
// 对分片路径，必须只读取本分片剩余字节，绝不能越过 end 写入相邻分片。
func copyResponseBody(r io.Reader, file *os.File, off, limit int64, onData func(int)) (int64, error) {
	bufp := getBuf()
	defer putBuf(bufp)
	buf := *bufp
	var written int64
	for limit < 0 || written < limit {
		readBuf := buf
		if limit >= 0 {
			remaining := limit - written
			if int64(len(readBuf)) > remaining {
				readBuf = readBuf[:remaining]
			}
		}
		n, rerr := r.Read(readBuf)
		if n > 0 {
			if _, werr := file.WriteAt(readBuf[:n], off+written); werr != nil {
				return written, werr
			}
			written += int64(n)
			if onData != nil {
				onData(n)
			}
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
	return written, nil
}

// Close 归还共享 Transport 引用。可以重复调用。
func (d *httpBase) Close() error {
	releaseHTTPTransport(d.transport)
	return nil
}

// httpDriver 为 HTTP 与 HTTPS 实现 ProtocolDriver：用 HEAD（失败时
// 回退为 1 字节的 Range GET）探测，再按字节区间分片下载。
type httpDriver struct {
	httpBase
}

// newHTTPDriver 是由 New(...) 调用的工厂入口。
func newHTTPDriver(raw string, auth AuthOptions) (ProtocolDriver, error) {
	base, err := newHTTPBase("http", raw, auth)
	if err != nil {
		return nil, err
	}
	return &httpDriver{httpBase: base}, nil
}

// Probe 发起 HEAD 请求（失败时回退为 1 字节的 Range GET）以探测
// 文件大小以及服务器是否支持字节区间（Range）。
func (d *httpDriver) Probe(ctx context.Context) (*DriverCapabilities, error) {
	headCtx := ctx
	cancelHead := func() {}
	if d.probeTimeout > 0 {
		headCtx, cancelHead = context.WithTimeout(ctx, d.probeTimeout)
	}
	req, err := http.NewRequestWithContext(headCtx, http.MethodHead, d.url, nil)
	if err != nil {
		cancelHead()
		return nil, fmt.Errorf("http probe build: %w", err)
	}
	d.decorate(req)

	resp, err := d.cli.Do(req)
	if err != nil {
		cancelHead()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// 部分服务器屏蔽 HEAD；改用最小的区间 GET 重试。
		return d.probeWithRange(ctx)
	}
	defer cancelHead()
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, newHTTPStatusError("http probe", resp)
	}

	caps := &DriverCapabilities{
		ServerInfo:   strings.Join(resp.Header["Server"], ", "),
		SupportRange: supportsRanges(resp),
		TotalSize:    parseInt64(resp.Header.Get("Content-Length")),
	}
	captureValidators(caps, resp.Header)
	// 用最小的实际 Range GET 验证能力。部分服务器会错误声明
	// Accept-Ranges，或由代理吞掉 Range；仅看 HEAD 头不足以保证安全。
	rangeCaps, rangeErr := d.probeWithRange(ctx)
	if rangeErr != nil {
		if caps.TotalSize < 0 {
			return nil, rangeErr
		}
		caps.SupportRange = false
		d.setValidators(caps.ETag, caps.LastModified)
		return caps, nil
	}
	caps.SupportRange = rangeCaps.SupportRange
	if caps.TotalSize < 0 {
		caps.TotalSize = rangeCaps.TotalSize
	}
	if rangeCaps.ETag != "" {
		caps.ETag = rangeCaps.ETag
	}
	if rangeCaps.LastModified != "" {
		caps.LastModified = rangeCaps.LastModified
	}
	d.setValidators(caps.ETag, caps.LastModified)
	return caps, nil
}

// probeWithRange 发起 1 字节的区间 GET，以促使服务器同时返回
// Content-Length 与 Accept-Ranges 字段。
func (d *httpBase) probeWithRange(ctx context.Context) (*DriverCapabilities, error) {
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
		return nil, newHTTPStatusError("http probe range", resp)
	}
	caps := &DriverCapabilities{
		SupportRange: resp.StatusCode == http.StatusPartialContent,
		ServerInfo:   strings.Join(resp.Header["Server"], ", "),
	}
	captureValidators(caps, resp.Header)
	d.setValidators(caps.ETag, caps.LastModified)
	if caps.SupportRange {
		start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != 0 || end != 0 {
			caps.SupportRange = false
		} else {
			caps.TotalSize = total
		}
	} else {
		caps.TotalSize = parseInt64(resp.Header.Get("Content-Length"))
	}
	// 最多消费 1 字节。服务端若忽略 Range 返回 200，不能把整个文件
	// 在“探测”阶段下载到 io.Discard。
	_, _ = io.CopyN(io.Discard, resp.Body, 1)
	return caps, nil
}

// supportsRanges 在服务器声明支持区间下载时返回 true。
// "bytes" 是规范取值；部分服务器返回 "1"，或干脆不返回该头。
func supportsRanges(resp *http.Response) bool {
	v := strings.ToLower(resp.Header.Get("Accept-Ranges"))
	if v == "" {
		return false
	}
	return v == "bytes" || v == "1"
}

// parseInt64 解析一个可能为空的头字段值为 int64，缺失或非法时返回 -1。
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

// parseTotalFromContentRange 解析形如 "bytes 0-0/12345" 的 Content-Range，
// 返回完整的总字节数 12345。
func parseTotalFromContentRange(s string) int64 {
	_, _, total, ok := parseContentRange(s)
	if !ok {
		return -1
	}
	return total
}

// parseContentRange 解析 "bytes START-END/TOTAL"；TOTAL 为 "*" 时返回 -1。
func parseContentRange(s string) (start, end, total int64, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(s), "bytes ") {
		return 0, 0, 0, false
	}
	s = strings.TrimSpace(s[len("bytes "):])
	slash := strings.IndexByte(s, '/')
	if slash < 0 {
		return 0, 0, 0, false
	}
	bounds := strings.TrimSpace(s[:slash])
	dash := strings.IndexByte(bounds, '-')
	if dash < 0 {
		return 0, 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(bounds[:dash]), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, 0, false
	}
	end, err = strconv.ParseInt(strings.TrimSpace(bounds[dash+1:]), 10, 64)
	if err != nil || end < start {
		return 0, 0, 0, false
	}
	totalText := strings.TrimSpace(s[slash+1:])
	if totalText == "*" {
		total = -1
	} else {
		total, err = strconv.ParseInt(totalText, 10, 64)
		if err != nil || total <= end {
			return 0, 0, 0, false
		}
	}
	return start, end, total, true
}
