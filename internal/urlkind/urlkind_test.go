package urlkind

import "testing"

func TestDetectKind(t *testing.T) {
	cases := []struct {
		raw  string
		hint ProtocolKind
		want ProtocolKind
		ok   bool
	}{
		{raw: "http://x/a.zip", want: ProtoHTTP, ok: true},
		{raw: "https://x/a.zip", want: ProtoHTTPS, ok: true},
		{raw: "ftp://x/a.zip", want: ProtoFTP, ok: true},
		{raw: "webdav://x/a", want: ProtoWebDAV, ok: true},
		{raw: "dav://x/a", want: ProtoWebDAV, ok: true},
		{raw: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", want: ProtoBT, ok: true},
		{raw: "http://x/a.torrent", want: ProtoBT, ok: true},
		{raw: "http://x/a.torrent?token=1", want: ProtoBT, ok: true},
		{raw: "http://x/a", hint: ProtoHTTP, want: ProtoHTTP, ok: true},
		{raw: "gopher://x/a", ok: false},
	}
	for _, c := range cases {
		got, err := DetectKind(c.raw, c.hint)
		if c.ok {
			if err != nil {
				t.Fatalf("DetectKind(%q,%q) 报错: %v", c.raw, c.hint, err)
			}
			if got != c.want {
				t.Fatalf("DetectKind(%q,%q) = %q, want %q", c.raw, c.hint, got, c.want)
			}
		} else if err == nil {
			t.Fatalf("DetectKind(%q,%q) 应当报错", c.raw, c.hint)
		}
	}
}

func TestIsBitTorrent(t *testing.T) {
	if !IsBitTorrent("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("磁力链接应识别为 BT")
	}
	if !IsBitTorrent("/tmp/a.torrent") {
		t.Fatal(".torrent 应识别为 BT")
	}
	if IsBitTorrent("http://x/a.zip") {
		t.Fatal("普通 URL 不应识别为 BT")
	}
}

func TestResolveURL(t *testing.T) {
	if got := ResolveURL("webdav://h/p", ProtoWebDAV); got != "https://h/p" {
		t.Fatalf("ResolveURL webdav = %q", got)
	}
	if got := ResolveURL("dav://h/p", ProtoWebDAV); got != "http://h/p" {
		t.Fatalf("ResolveURL dav = %q", got)
	}
	if got := ResolveURL("https://h/p", ProtoHTTPS); got != "https://h/p" {
		t.Fatalf("ResolveURL https = %q", got)
	}
}

func TestBittorrentDefaultDir(t *testing.T) {
	if got := BittorrentDefaultDir("http://x/a.zip"); got != "" {
		t.Fatalf("非 BT 应返回空串, got %q", got)
	}
	if got := BittorrentDefaultDir("http://x/movie.torrent"); got != "movie" {
		t.Fatalf("torrent 目录名 = %q", got)
	}
	if got := BittorrentDefaultDir("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=My%20Movie"); got != "My Movie" {
		t.Fatalf("磁力目录名 = %q", got)
	}
}
