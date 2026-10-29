// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

// ErrorInfo 是稳定、可本地化的错误分类。Kind 只包含 ASCII slug，UI
// 根据它选择翻译文案；原始错误仍单独保留，便于排障。
type ErrorInfo struct {
	Kind      string
	Retryable bool
}

// ClassifyError 把驱动、文件系统和网络错误归入用户可采取行动的分类。
func ClassifyError(err error) ErrorInfo {
	if err == nil {
		return ErrorInfo{}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrInsufficientDiskSpace), strings.Contains(msg, "no space left"):
		return ErrorInfo{Kind: "disk_full"}
	case errors.Is(err, ErrDigestMismatch):
		return ErrorInfo{Kind: "checksum_mismatch"}
	case errors.Is(err, ErrBodyStalled):
		return ErrorInfo{Kind: "source_stalled", Retryable: true}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return ErrorInfo{Kind: "timeout", Retryable: true}
	case errors.Is(err, os.ErrPermission):
		return ErrorInfo{Kind: "permission"}
	case errors.Is(err, ErrRangeUnavailable), errors.Is(err, ErrResumeUnavailable):
		return ErrorInfo{Kind: "resume_unsupported", Retryable: true}
	}

	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) ||
		errors.As(err, &certInvalid) || strings.Contains(msg, "certificate") ||
		strings.Contains(msg, "tls: ") {
		return ErrorInfo{Kind: "tls_certificate"}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) || strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "server misbehaving") {
		return ErrorInfo{Kind: "dns", Retryable: dnsErr == nil || dnsErr.Temporary()}
	}
	var tlsRecord tls.RecordHeaderError
	if errors.As(err, &tlsRecord) {
		return ErrorInfo{Kind: "tls_handshake", Retryable: true}
	}

	switch code := HTTPStatus(err); {
	case code == http.StatusUnauthorized, code == http.StatusProxyAuthRequired:
		return ErrorInfo{Kind: "authentication"}
	case code == http.StatusForbidden:
		return ErrorInfo{Kind: "forbidden"}
	case code == http.StatusNotFound, code == http.StatusGone:
		return ErrorInfo{Kind: "not_found"}
	case code == http.StatusTooManyRequests:
		return ErrorInfo{Kind: "rate_limited", Retryable: true}
	case code >= 500:
		return ErrorInfo{Kind: "server_error", Retryable: true}
	}

	if errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrorInfo{Kind: "connection_interrupted", Retryable: true}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ErrorInfo{Kind: "network", Retryable: netErr.Timeout() || netErr.Temporary()}
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "network is unreachable") {
		return ErrorInfo{Kind: "network", Retryable: true}
	}
	if strings.Contains(msg, "resource validator changed") || strings.Contains(msg, "source changed") {
		return ErrorInfo{Kind: "source_changed", Retryable: true}
	}
	return ErrorInfo{Kind: "unknown"}
}
