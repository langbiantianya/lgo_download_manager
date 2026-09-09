// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build darwin

package nativefolder

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PickFolder opens the macOS NSOpenPanel directory picker driven via
// AppleScript. AppleScript is the standard, dependency-free path: it
// runs in-process inside the user's Aqua session and shows the system
// folder chooser sheet attached to the frontmost Finder window.
//
// If initialDir is non-empty the chooser starts in that directory.
// Otherwise the system picks a sensible default.
//
// The picker is cancelled by returning ErrCancelled.
func PickFolder(initialDir string) (string, error) {
	// We build the script as a literal so the path interpolation stays
	// safe. The shell quoting is double-quoted AppleScript string.
	locationExpr := ""
	if initialDir != "" {
		// Escape backslash and double-quote for the AppleScript literal.
		escaped := strings.ReplaceAll(initialDir, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		locationExpr = fmt.Sprintf("of POSIX file %q\n\t\t", escaped)
	}

	script := fmt.Sprintf(`
try
	set theFolder to choose folder with prompt "选择保存位置" %s
	return POSIX path of theFolder
on error number -128
	error number -128
end try
`, locationExpr)

	// Bound the run so a stuck Apple event cannot wedge the UI.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// AppleScript error -128 is the user-cancelled code; the
		// "error number -128" reraise keeps the failure on the stderr
		// channel as well, but we map it explicitly here for callers
		// that probe IsCancelled.
		out := strings.TrimSpace(stdout.String())
		if out == "" && strings.Contains(strings.ToLower(stderr.String()), "user canceled") {
			return "", ErrCancelled
		}
		if out == "" {
			// Empty result with no stderr: assume cancel.
			return "", ErrCancelled
		}
		return "", fmt.Errorf("osascript: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
	}

	path := strings.TrimRight(stdout.String(), "\n\r")
	if path == "" {
		return "", ErrCancelled
	}
	return path, nil
}
