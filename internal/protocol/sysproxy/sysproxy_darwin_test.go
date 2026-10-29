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
	if cfg.Bypass != "localhost,127.0.0.1" {
		t.Errorf("Bypass = %q, want parsed ExceptionsList", cfg.Bypass)
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

func TestParseScutilKeepsHTTPSAndHTTPEndpoints(t *testing.T) {
	const sample = `HTTPEnable : 1
HTTPProxy : 127.0.0.1
HTTPPort : 8080
HTTPSEnable : 1
HTTPSProxy : 127.0.0.1
HTTPSPort : 8443
SOCKSEnable : 0
`
	cfg, ok := parseScutil(sample)
	if !ok {
		t.Fatal("parseScutil returned ok=false")
	}
	if cfg.HTTPProxyURL != "http://127.0.0.1:8080" {
		t.Errorf("HTTPProxyURL = %q", cfg.HTTPProxyURL)
	}
	if cfg.HTTPSProxyURL != "http://127.0.0.1:8443" {
		t.Errorf("HTTPSProxyURL = %q", cfg.HTTPSProxyURL)
	}
}

func TestParseScutilSimpleHostnamesUseLocalMarker(t *testing.T) {
	const sample = `HTTPEnable : 1
HTTPProxy : 127.0.0.1
HTTPPort : 8080
ExcludeSimpleHostnames : 1
`
	cfg, ok := parseScutil(sample)
	if !ok {
		t.Fatal("parseScutil returned ok=false")
	}
	if cfg.Bypass != "<local>" {
		t.Errorf("Bypass = %q, want <local>", cfg.Bypass)
	}
}

func TestParseScutilPACURL(t *testing.T) {
	const sample = `ProxyAutoConfigEnable : 1
ProxyAutoConfigURLString : http://example.com/proxy.pac
`
	cfg, ok := parseScutil(sample)
	if !ok || cfg.Source != "darwin-pac" {
		t.Errorf("PAC should be reported as configured: %+v ok=%v", cfg, ok)
	}
	if cfg.AutoConfigURL != "http://example.com/proxy.pac" {
		t.Errorf("AutoConfigURL = %q", cfg.AutoConfigURL)
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
