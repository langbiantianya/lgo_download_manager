// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package urllauncher

import (
	"testing"
)

func TestHandleURL(t *testing.T) {
	rawURL := "lgom://download?url=https%3A%2F%2Fexample.com%2Ffile.zip&name=file.zip&ua=Mozilla%2F5.0"
	req, err := HandleURL(rawURL)
	if err != nil {
		t.Fatalf("HandleURL failed: %v", err)
	}
	if req.URL != "https://example.com/file.zip" {
		t.Errorf("URL = %q, want %q", req.URL, "https://example.com/file.zip")
	}
	if req.Name != "file.zip" {
		t.Errorf("Name = %q, want %q", req.Name, "file.zip")
	}
	if req.UA != "Mozilla/5.0" {
		t.Errorf("UA = %q, want %q", req.UA, "Mozilla/5.0")
	}
}

func TestHandleURLInvalid(t *testing.T) {
	_, err := HandleURL("http://example.com")
	if err == nil {
		t.Error("Expected error for invalid scheme")
	}
}
