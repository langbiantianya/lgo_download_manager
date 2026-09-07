// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package urllauncher

import (
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

// Single-instance lock for Windows. We use a per-session named mutex instead
// of flock(2) because flock is not portable.
//
// Two important Windows-specific constraints are baked into mutexKernelName:
//   - The kernel namespace prefix must be reachable from a normal interactive
//     user. `Global\` requires SeCreateGlobalPrivilege (services only) and
//     fails with ERROR_PATH_NOT_FOUND otherwise. We use `Local\` which is the
//     per-session namespace and works for every user.
//   - Backslashes inside an NT object name are namespace separators, so we
//     must not embed the raw lockPath (which contains C:\...). We sanitize
//     the path into a flat identifier.
//
// URL forwarding (lgom:// URL pass-through from a secondary instance to the
// primary) is currently NOT implemented on Windows: there is no Unix domain
// socket equivalent. ListenAndServe is therefore a no-op and SendURL returns
// an error so the secondary instance fails fast and reports the limitation,
// instead of silently dropping the user's URL.
var (
	winMutexNameMu sync.Mutex
)

// mutexKernelName converts lockPath into a kernel-object name.
//
// Format: "Local\lgo_dm_instance_<sanitized>"
//
// Sanitization rules:
//   - Strip Windows drive letter + colon ("C:" → "").
//   - Replace every path separator '\' with '_'.
//   - Keep only [A-Za-z0-9_-]; everything else becomes '_'.
//   - Collapse runs of '_'.
//   - Trim leading/trailing '_'.
//   - Hard-cap at 200 chars to stay well under the 260-char NT path limit.
//
// The result is stable for a given install directory and unique across users
// (each user has a different home dir and therefore a different sanitized
// string), but identical for repeated invocations by the same user.
func mutexKernelName() string {
	const prefix = `Local\lgo_dm_instance_`
	const maxTail = 200

	p := lockPath
	if len(p) >= 2 && p[1] == ':' {
		p = p[2:] // strip drive letter "C:" / "D:" / …
	}
	var b strings.Builder
	b.Grow(len(p))
	lastUnderscore := false
	for _, r := range p {
		switch {
		case r == '\\' || r == '/':
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '.':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	tail := strings.Trim(b.String(), "_")
	if len(tail) > maxTail {
		tail = tail[:maxTail]
	}
	return prefix + tail
}

// AcquireLock uses a Windows per-session named mutex so the single-instance
// guarantee works on Windows even though flock(2) is unavailable.
//
// Returns:
//   - isPrimary=true, release!=nil, err=nil  → this instance is the primary.
//   - isPrimary=false, release=nil, err=nil  → another instance already holds
//     the lock; the caller should act as a forwarder.
//   - isPrimary=false, release=nil, err!=nil  → an OS-level error occurred.
func AcquireLock() (isPrimary bool, release func(), err error) {
	winMutexNameMu.Lock()
	namePtr, err := windows.UTF16PtrFromString(mutexKernelName())
	winMutexNameMu.Unlock()
	if err != nil {
		return false, nil, fmt.Errorf("urllauncher: invalid mutex name: %w", err)
	}

	// SECURITY_ATTRIBUTES defaults: created in the per-session Local\
	// namespace without an inheritable or restrictive DACL. Any process in
	// the same user session can probe ownership; that is sufficient for the
	// single-instance contract and avoids the SeCreateGlobalPrivilege
	// requirement of Global\.
	handle, err := windows.CreateMutex(nil, false, namePtr)
	if err != nil {
		if errno, ok := err.(windows.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
			// CreateMutex still returns a valid handle to the existing mutex;
			// we own a reference and must release it before returning.
			windows.CloseHandle(handle)
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("urllauncher: CreateMutex: %w", err)
	}

	// We hold the handle. Keep it alive until release() runs.
	return true, func() {
		windows.CloseHandle(handle)
	}, nil
}

// ListenAndServe is a no-op on Windows: URL forwarding via Unix domain
// socket is not available. Callers (main.go) already wrap the call in a
// goroutine that only logs on error, so returning nil keeps the program
// running and surfaces no spurious log noise.
func ListenAndServe(onURL func(url string)) error {
	// URL forwarding on Windows is not implemented in this package.
	// Intentional no-op; reserved for a future Windows-native IPC channel.
	return nil
}

// SendURL returns an error on Windows because there is no Unix socket to
// dial. main.go treats this as fatal when the caller is a non-primary
// instance holding lgom:// URLs to forward.
func SendURL(url string) error {
	_ = url
	return fmt.Errorf("urllauncher: URL forwarding is not supported on Windows")
}