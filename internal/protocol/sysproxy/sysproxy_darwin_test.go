// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build darwin

package sysproxy

import "testing"

func TestParseScutilHTTPS(t *testing.T) {
	const sample = `<dictionary> {
  HTTPEnable : 0
  HTTPProxy :
  HTTPPort : 0
  HTTPSEnable : 1
  HTTPSProxy : 127.0.0.1
  HTTPSPort : 7890
  SOCKSEnable : 0
  ExceptionsList : <array> {
    0 : localhost
    1 : 127.0.0.1
  }
  ProxyAutoConfigEnable : 0
}
`
	cfg, ok := parseScutil(sample)
	if !ok {
		t.Fatal("parseScutil returned ok=false")
	}
	if cfg.ProxyURL != "http://127.0.0.1:7890" {
		t.Errorf("ProxyURL = %q, want http://127.0.0.1:7890", cfg.ProxyURL)
	}
	if cfg.Source != "darwin-scutil" {
		t.Errorf("Source = %q, want darwin-scutil", cfg.Source)
	}
}

func TestParseScutilSOCKS(t *testing.T) {
	const sample = `HTTPEnable : 0
HTTPSEnable : 0
SOCKSEnable : 1
SOCKSProxy : 127.0.0.1
SOCKSPort : 1080
`
	cfg, ok := parseScutil(sample)
	if !ok || cfg.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Errorf("SOCKS proxy parse failed: %+v ok=%v", cfg, ok)
	}
}

func TestParseScutilPACUnsupported(t *testing.T) {
	const sample = `ProxyAutoConfigEnable : 1
ProxyAutoConfigURLString : http://example.com/proxy.pac
`
	cfg, ok := parseScutil(sample)
	if !ok || cfg.Source != "darwin-pac-unsupported" {
		t.Errorf("PAC should be reported as unsupported: %+v ok=%v", cfg, ok)
	}
	if cfg.ProxyURL != "" {
		t.Errorf("PAC mode should not surface a ProxyURL, got %q", cfg.ProxyURL)
	}
}

func TestParseScutilAllDisabled(t *testing.T) {
	const sample = `HTTPEnable : 0
HTTPSEnable : 0
SOCKSEnable : 0
`
	cfg, ok := parseScutil(sample)
	if !ok || cfg.Source != "darwin-scutil" || cfg.ProxyURL != "" {
		t.Errorf("all-disabled parse wrong: %+v ok=%v", cfg, ok)
	}
}
