// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package version exposes build-time metadata. Defaults are placeholders;
// the real values are injected via -ldflags "-X lgo_download_manager/internal/version.Version=…"
// by the Makefile.
package version

import "fmt"

// These variables are overridden at link time by the build script.
// Keep the defaults human-readable so an unstripped binary still tells you
// it was a dev build rather than a tagged release.
var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String renders the full build identity.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}