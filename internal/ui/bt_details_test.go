// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// btStubService 是 Service 的最小实现:只有 BTTaskDetail 有内容,其余
// 方法在 BT 详情面板的测试里不会被走到。
type btStubService struct {
	detail *bittorrent.Detail
	err    error
}

func (s *btStubService) List(store.StatusFilter, store.TaskSort) ([]*store.Task, error) {
	return nil, nil
}
func (s *btStubService) Subscribe() (<-chan scheduler.Event, func()) {
	return make(chan scheduler.Event), func() {}
}
func (s *btStubService) AddTask(AddTaskInput) (*store.Task, error) { return nil, nil }
func (s *btStubService) Start(string) error                        { return nil }
func (s *btStubService) Pause(string) error                        { return nil }
func (s *btStubService) Delete(string) error                       { return nil }
func (s *btStubService) IsPreparing(string) bool                   { return false }
func (s *btStubService) Probe(string) (int64, error)               { return 0, nil }
func (s *btStubService) BTTaskDetail(string) (*bittorrent.Detail, error) {
	return s.detail, s.err
}
func (s *btStubService) Settings() store.Settings          { return store.Settings{} }
func (s *btStubService) SaveSettings(store.Settings) error { return nil }
func (s *btStubService) Close() error                      { return nil }

// samplePieceBuckets 造一份分片热力图数据:前面四成已经下完,中间三成
// 下了一半(逐步推进),后面三成还没动——这样图上能同时看到「完成 /
// 在传 / 没动」三种颜色。
func samplePieceBuckets() []uint8 {
	out := make([]uint8, 256)
	for i := range out {
		switch {
		case i < 102:
			out[i] = 255
		case i < 180:
			out[i] = uint8(255 - (i-102)*3)
		default:
			out[i] = 0
		}
	}
	return out
}

// sampleBTDetail 造一份覆盖面板各字段的详情快照。
func sampleBTDetail() *bittorrent.Detail {
	return &bittorrent.Detail{
		Name:         "debian-13.7.0-amd64-netinst.iso",
		InfoHash:     "0123456789abcdef0123456789abcdef01234567",
		Total:        1000,
		Downloaded:   400,
		Uploaded:     200,
		SpeedBPS:     1024,
		UploadBPS:    512,
		Peers:        7,
		TotalPeers:   30,
		Seeders:      2,
		Leechers:     5,
		PieceLength:  256,
		Pieces:       10,
		PiecesDone:   4,
		Seeding:      true,
		Complete:     true,
		Trackers:     []string{"udp://tracker.example:6969/announce"},
		PieceBuckets: samplePieceBuckets(),
		Files: []bittorrent.FileProgress{
			{Path: "debian-13.7.0-amd64-netinst.iso", Length: 1000, Completed: 400},
			{Path: "README", Length: 100, Completed: 100},
		},
	}
}

// TestBTDetailsPanelShowsTorrentFacts 校验 BT 详情面板把种子的实时信息
// 摆对了位置:infohash、分片、上下行速度、peer 构成、做种状态、tracker
// 与文件清单——这是「BT 详情不该和 HTTP 详情一样」的核心。
func TestBTDetailsPanelShowsTorrentFacts(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	tk := &store.Task{
		ID:       "bt-1",
		URL:      "https://example.com/a.torrent",
		SavePath: filepath.Join("C:", "dl", "a"),
		Protocol: "BT",
	}
	p := newBTDetailsPanel(tk)
	d := sampleBTDetail()
	p.update(d)

	if p.lblName.Text != d.Name {
		t.Errorf("name = %q, want %q", p.lblName.Text, d.Name)
	}
	if p.lblInfoHash.Text != d.InfoHash {
		t.Errorf("infohash = %q, want %q", p.lblInfoHash.Text, d.InfoHash)
	}
	if p.lblTotal.Text != "1000 B" {
		t.Errorf("total = %q, want 1000 B", p.lblTotal.Text)
	}
	if p.lblDownloaded.Text != "400 B" {
		t.Errorf("downloaded = %q, want 400 B", p.lblDownloaded.Text)
	}
	if p.lblUploaded.Text != "200 B" {
		t.Errorf("uploaded = %q, want 200 B", p.lblUploaded.Text)
	}
	// 分享率 = 已上传 / 已下载 = 200/400。
	if p.lblRatio.Text != "0.50" {
		t.Errorf("ratio = %q, want 0.50", p.lblRatio.Text)
	}
	if p.lblPeers.Text != "7" || p.lblTotalPeers.Text != "30" {
		t.Errorf("peers = %q/%q, want 7/30", p.lblPeers.Text, p.lblTotalPeers.Text)
	}
	if p.lblSeeders.Text != "2" || p.lblLeechers.Text != "5" {
		t.Errorf("seeders/leechers = %q/%q, want 2/5", p.lblSeeders.Text, p.lblLeechers.Text)
	}
	if p.lblPieceLen.Text != "256 B" {
		t.Errorf("piece length = %q, want 256 B", p.lblPieceLen.Text)
	}
	if p.lblPieces.Text != "10" {
		t.Errorf("pieces = %q, want 10", p.lblPieces.Text)
	}
	if p.lblPiecesDone.Text != "4 / 10" {
		t.Errorf("pieces done = %q, want 4 / 10", p.lblPiecesDone.Text)
	}
	if p.lblState.Text != ilocale.T("details.bt.state.seeding") {
		t.Errorf("state = %q, want the seeding label", p.lblState.Text)
	}
	if p.lblTrackers.Text != d.Trackers[0] {
		t.Errorf("trackers = %q, want %q", p.lblTrackers.Text, d.Trackers[0])
	}
	// 文件清单:两个文件各一行,进度条按已完成比例。
	if len(p.fileRows) != 2 {
		t.Fatalf("file rows = %d, want 2", len(p.fileRows))
	}
	if p.fileRows[0].name.Text != d.Files[0].Path {
		t.Errorf("file name = %q, want %q", p.fileRows[0].name.Text, d.Files[0].Path)
	}
	if p.fileRows[0].bar.Value != 0.4 {
		t.Errorf("file 0 progress = %v, want 0.4", p.fileRows[0].bar.Value)
	}
	if p.fileRows[1].bar.Value != 1 {
		t.Errorf("file 1 progress = %v, want 1", p.fileRows[1].bar.Value)
	}
}

// TestBTDetailsPanelTracksFileCount 校验文件清单随元数据变化重建:磁力
// 链接刚起步时 0 个文件,拿到元数据后变成 N 个——行数不变时只更新进度,
// 行数变了才重建,否则每秒都在重建 widget 树。
func TestBTDetailsPanelTracksFileCount(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	p := newBTDetailsPanel(&store.Task{ID: "bt-1", Protocol: "BT"})
	p.update(&bittorrent.Detail{})
	if len(p.fileRows) != 0 {
		t.Fatalf("file rows before metadata = %d, want 0", len(p.fileRows))
	}
	p.update(sampleBTDetail())
	if len(p.fileRows) != 2 {
		t.Fatalf("file rows after metadata = %d, want 2", len(p.fileRows))
	}
	// 行数不变:只更新进度,行对象被复用。
	first := p.fileRows[0]
	p.update(sampleBTDetail())
	if len(p.fileRows) != 2 || p.fileRows[0] != first {
		t.Error("rows should be reused while the file count stays the same")
	}
}

// TestBTDetailsPanelInactive 校验任务不在运行时状态切到「会话已结束」,
// 并把原因写进提示行——任务暂停或已完成且未做种时都走这条路。
func TestBTDetailsPanelInactive(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	p := newBTDetailsPanel(&store.Task{ID: "bt-1", Protocol: "BT"})
	p.fetch(&btStubService{err: errors.New("not active")}, "bt-1")
	if p.lblState.Text != ilocale.T("details.bt.state.inactive") {
		t.Errorf("state = %q, want the inactive label", p.lblState.Text)
	}
	if p.lblNote.Text == "" {
		t.Error("the failure reason should be shown to the user")
	}
}

// TestShowChunkDetailsPicksBTPanel 校验详情窗口按协议分派:BT 任务拿到
// BT 专属面板,HTTP 任务仍是原来的分块马赛克。
func TestShowChunkDetailsPicksBTPanel(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	btTask := &store.Task{
		ID:       "bt-1",
		URL:      "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		SavePath: filepath.Join("C:", "dl", "bt"),
		Protocol: "BT",
	}
	p := showChunkDetails(btTask, &btStubService{detail: sampleBTDetail()}, nil)
	if p == nil {
		t.Fatal("a BT task must get the BT details panel, not the generic one")
	}
	// 窗口确实开出来了。
	wins := append([]fyne.Window(nil), fyne.CurrentApp().Driver().AllWindows()...)
	if len(wins) == 0 {
		t.Error("the details window was not created")
	}
	// 关掉详情窗口(最后创建的那个),让轮询 goroutine 退出。
	// AllWindows 返回的是驱动内部切片,关窗会就地改动它——必须先复制,
	// 否则边遍历边关会把后面的元素变成 nil;也不能把驱动自带的占位
	// 窗口一起关掉,否则后续渲染会找不到画布。
	if len(wins) > 0 {
		wins[len(wins)-1].Close()
	}
	time.Sleep(50 * time.Millisecond)

	httpTask := &store.Task{
		ID:       "http-1",
		URL:      "https://example.com/a.zip",
		SavePath: filepath.Join("C:", "dl", "a.zip"),
		Protocol: "HTTPS",
	}
	if got := showChunkDetails(httpTask, &btStubService{}, nil); got != nil {
		t.Error("a plain HTTP task must not get the BT details panel")
	}
}

// TestBTFormatHelpers 校验面板里的格式化辅助:0 字节要显示成 "0 B" 而不是
// 「未知大小」(BT 面板里 0 是有意义的),分享率在还没下载任何东西时是 "-"。
func TestBTFormatHelpers(t *testing.T) {
	if got := formatBytes(0); got != "0 B" {
		t.Errorf("formatBytes(0) = %q, want 0 B", got)
	}
	if got := formatBytes(1024); got != "1.0 KB" {
		t.Errorf("formatBytes(1024) = %q, want 1.0 KB", got)
	}
	if got := btFormatRatio(100, 0); got != "-" {
		t.Errorf("btFormatRatio(100, 0) = %q, want -", got)
	}
	if got := btFormatRatio(200, 400); got != "0.50" {
		t.Errorf("btFormatRatio(200, 400) = %q, want 0.50", got)
	}
}

// TestBTDetailsPanelFeedsPieceMosaic 校验分片进度真的接到了热力图上:
// 面板刷新时要把快照里的分片桶交给热力图,不然图上永远是空的——
// 「BT 详情只有完整进度、没有分片热力图」正是要修的问题。
func TestBTDetailsPanelFeedsPieceMosaic(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	p := newBTDetailsPanel(&store.Task{ID: "bt-1", Protocol: "BT"})
	d := sampleBTDetail()
	p.update(d)

	got, _ := p.mosaic.snapshot()
	if len(got) != len(d.PieceBuckets) {
		t.Fatalf("mosaic buckets = %d, want %d", len(got), len(d.PieceBuckets))
	}
	for i := range d.PieceBuckets {
		if got[i] != d.PieceBuckets[i] {
			t.Fatalf("bucket %d = %d, want %d", i, got[i], d.PieceBuckets[i])
		}
	}
}

// TestBTDetailsPanelRenders 把 BT 详情面板渲染成图片,断言画面上确实有
// 东西(不是一整片空白)——面板的 widget 树一旦摆错(尺寸为 0、内容被挤
// 出可视区),文字还在但画面会是空的,只有渲染出来才看得出来。
//
// 需要留图时用环境变量指定落盘路径:
//	LGDM_BT_PANEL_PNG=out.png go test ./internal/ui -run TestBTDetailsPanelRenders
func TestBTDetailsPanelRenders(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	tk := &store.Task{
		ID:       "bt-1",
		URL:      "https://cdimage.debian.org/debian-cd/current/amd64/bt-cd/debian-13.7.0-amd64-netinst.iso.torrent",
		SavePath: filepath.Join("C:", "Users", "lbty", "Downloads", "debian-13.7.0-amd64-netinst.iso"),
		Protocol: "BT",
	}
	p := newBTDetailsPanel(tk)
	p.update(sampleBTDetail())

	w := test.NewWindow(p.content())
	w.Resize(fyne.NewSize(780, 720))
	img := w.Canvas().Capture()
	if img == nil {
		t.Fatal("canvas capture returned nil")
	}
	bounds := img.Bounds()
	if bounds.Dx() < 100 || bounds.Dy() < 100 {
		t.Fatalf("rendered image is %dx%d, want at least 100x100", bounds.Dx(), bounds.Dy())
	}

	// 画面上必须有非背景像素:全是背景色说明 widget 树没画出来。
	distinct := map[uint32]int{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 3 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 3 {
			r, g, b, _ := img.At(x, y).RGBA()
			distinct[uint32(r)<<16|uint32(g)<<8|uint32(b)]++
		}
	}
	if len(distinct) < 3 {
		t.Fatalf("rendered image has %d distinct colors, looks blank", len(distinct))
	}

	if out := os.Getenv("LGDM_BT_PANEL_PNG"); out != "" {
		f, err := os.Create(out)
		if err != nil {
			t.Fatalf("create %s: %v", out, err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatalf("encode png: %v", err)
		}
		t.Logf("wrote %s (%dx%d)", out, bounds.Dx(), bounds.Dy())
	}
}
