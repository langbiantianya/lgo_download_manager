// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !windows && !darwin

package nativefolder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PickFolder opens the native GTK (zenity) or Qt (kdialog) folder
// chooser, preferring zenity because it ships with the GNOME/GTK stack
// most desktop Linux distros default to. Both tools call into the
// platform's standard freedesktop file chooser dialog — i.e. the same
// dialog the user's file manager itself uses for "Open with…".
//
// If neither tool is on PATH the picker fails with an explanatory
// error so the caller can fall back to a manual text entry.
func PickFolder(initialDir string) (string, error) {
	if path, err := pickZenity(initialDir); err == nil || IsCancelled(err) {
		return path, err
	} else if isMissingTool(err) {
		if path, err := pickKDialog(initialDir); err == nil || IsCancelled(err) {
			return path, err
		} else if !isMissingTool(err) {
			return "", err
		}
		return "", fmt.Errorf("nativefolder: no GTK/Qt folder picker installed (install zenity or kdialog)")
	} else {
		return "", err
	}
}

func pickZenity(initialDir string) (string, error) {
	args := []string{
		"--file-selection",
		"--directory",
		"--title=选择保存位置",
	}
	if initialDir != "" {
		args = append(args, "--filename="+filepath.Join(initialDir, "")+"/")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "zenity", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	switch {
	case err == nil:
		if out == "" {
			return "", ErrCancelled
		}
		return out, nil
	case isCancelExit(err):
		return "", ErrCancelled
	case isMissingTool(err):
		return "", err
	default:
		return "", fmt.Errorf("zenity: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
	}
}

func pickKDialog(initialDir string) (string, error) {
	args := []string{
		"--getexistingdirectory",
		"--title", "选择保存位置",
	}
	if initialDir != "" {
		args = append(args, initialDir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kdialog", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	switch {
	case err == nil:
		if out == "" {
			return "", ErrCancelled
		}
		return out, nil
	case isCancelExit(err):
		return "", ErrCancelled
	case isMissingTool(err):
		return "", err
	default:
		return "", fmt.Errorf("kdialog: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
	}
}

// isMissingTool recognises the error returned by exec when the
// requested binary cannot be found on PATH.
func isMissingTool(err error) bool {
	var ee *exec.Error
	return errors.As(err, &ee) && ee.Err == exec.ErrNotFound
}

// isCancelExit recognises a non-zero exit with code 1 / 252 / 255 from
// zenity or kdialog when the user dismisses the dialog.
func isCancelExit(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	switch ee.ExitCode() {
	case 1, 252, 255:
		return true
	}
	return false
}
