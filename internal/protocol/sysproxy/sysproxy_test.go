// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package sysproxy

import "testing"

func TestNormalizeBypass(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"only_comma", ",,,", ""},
		{"spaces", " a , b ,c ", "a,b,c"},
		{"semicolon", "a;b;c", "a,b,c"},
		{"mixed", " a ; b , c ;", "a,b,c"},
		{"already_clean", "example.com,*.lan", "example.com,*.lan"},
		{"only_whitespace", "  ,  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeBypass(tt.in)
			if got != tt.want {
				t.Errorf("NormalizeBypass(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestConfigString(t *testing.T) {
	if got := (Config{}).String(); got != "direct" {
		t.Errorf("empty Config.String() = %q, want direct", got)
	}
	if got := (Config{ProxyURL: "http://1.2.3.4:7890"}).String(); got != "http://1.2.3.4:7890 via system" {
		t.Errorf("no-source Config.String() = %q", got)
	}
	if got := (Config{ProxyURL: "http://1.2.3.4:7890", Source: "gnome"}).String(); got != "http://1.2.3.4:7890 via gnome" {
		t.Errorf("gnome Config.String() = %q", got)
	}
}
