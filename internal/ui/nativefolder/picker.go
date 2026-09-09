// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package nativefolder exposes a single PickFolder entry point that opens
// the operating system's native folder-selection dialog.
//
// The Windows backend uses the modern IFileOpenDialog COM interface (the
// same dialog Windows Explorer itself presents for "Choose a folder");
// the macOS backend drives NSOpenPanel via osascript; the Linux backend
// shells out to zenity (with kdialog as fallback). All backends are
// implemented as separate files gated by build tags so this file carries
// only the shared contract.
package nativefolder

// ErrCancelled is returned when the user dismisses the dialog without
// selecting a folder. Callers should treat it as a non-error.
var ErrCancelled = errCancelled{}

type errCancelled struct{}

func (errCancelled) Error() string { return "folder selection cancelled" }

// IsCancelled reports whether err originated from the user cancelling
// the dialog.
func IsCancelled(err error) bool {
	_, ok := err.(errCancelled)
	return ok
}
