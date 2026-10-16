// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package bittorrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

// requireLoopback 在无法监听回环端口的环境(受限容器/沙箱)里跳过需要真实
// 网络栈的测试。CI 与本机开发环境都能监听,因此这些用例默认会真正跑起来。
func requireLoopback(t *testing.T) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listening unavailable in this environment: %v", err)
	}
	_ = l.Close()
}

// fakePayload 生成确定性的测试数据(不依赖随机源,失败可复现)。
func fakePayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*31 + 7) % 251)
	}
	return b
}

// makeSingleFileTorrent 造一个单文件种子的 metainfo 与 .torrent 字节流。
func makeSingleFileTorrent(t *testing.T, name string, content []byte, pieceLength int64) (*metainfo.MetaInfo, []byte) {
	t.Helper()
	info := metainfo.Info{
		Name:        name,
		Length:      int64(len(content)),
		PieceLength: pieceLength,
	}
	for off := int64(0); off < int64(len(content)); off += pieceLength {
		end := off + pieceLength
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		sum := sha1.Sum(content[off:end])
		info.Pieces = append(info.Pieces, sum[:]...)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	data, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatalf("marshal metainfo: %v", err)
	}
	return mi, data
}

// TestTorrentFileInfo 校验 .torrent 解析出的种子名与总体积(体积预览
// 与默认目录名都依赖它)。
func TestTorrentFileInfo(t *testing.T) {
	content := fakePayload(300 << 10)
	_, data := makeSingleFileTorrent(t, "hello.bin", content, 64<<10)

	name, total, err := TorrentFileInfo(data)
	if err != nil {
		t.Fatalf("TorrentFileInfo: %v", err)
	}
	if name != "hello.bin" {
		t.Errorf("name = %q, want hello.bin", name)
	}
	if total != int64(len(content)) {
		t.Errorf("total = %d, want %d", total, len(content))
	}

	if _, _, err := TorrentFileInfo([]byte("not a torrent")); err == nil {
		t.Error("expected error for garbage input")
	}
}

// TestManagerRejectsDuplicateInfohash 校验同一个 infohash 不会被添加两次:
// 第二个任务必须拿到 ErrAlreadyAdded,否则两个任务会共享同一个 Torrent,
// 进度互相覆盖。
func TestManagerRejectsDuplicateInfohash(t *testing.T) {
	requireLoopback(t)
	content := fakePayload(200 << 10)
	_, data := makeSingleFileTorrent(t, "dup.bin", content, 32<<10)

	m, err := New(Config{MaxPeersPerTorrent: 8})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	first, err := m.AddTorrentBytes(context.Background(), data, t.TempDir())
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if m.Active() != 1 {
		t.Errorf("Active() = %d, want 1", m.Active())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := first.WaitInfo(ctx); err != nil {
		t.Fatalf("WaitInfo: %v", err)
	}
	if _, err := m.AddTorrentBytes(context.Background(), data, t.TempDir()); !errors.Is(err, ErrAlreadyAdded) {
		t.Fatalf("second add err = %v, want ErrAlreadyAdded", err)
	}
	// Close 之后种子被摘除,活跃数归零,同一个种子可以重新添加(恢复下载)。
	first.Close()
	if got := m.Active(); got != 0 {
		t.Errorf("Active() after close = %d, want 0", got)
	}
	again, err := m.AddTorrentBytes(context.Background(), data, t.TempDir())
	if err != nil {
		t.Fatalf("re-add after close: %v", err)
	}
	again.Close()
}

// TestDownloadFromLoopbackPeer 是端到端用例:一个本地做种端 + 我们自己的
// Manager,验证种子能真正下载完成、进度/文件列表正确、内容与源一致。
func TestDownloadFromLoopbackPeer(t *testing.T) {
	requireLoopback(t)

	content := fakePayload(768<<10 + 123)
	const pieceLength = 128 << 10
	mi, torrentBytes := makeSingleFileTorrent(t, "hello.bin", content, pieceLength)

	// ---- 做种端:直接用 anacrolix 客户端,数据已在 DataDir 里 ----
	seedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(seedDir, "hello.bin"), content, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	scfg := torrent.NewDefaultClientConfig()
	scfg.DataDir = seedDir
	scfg.ListenPort = 0 // 随机端口
	scfg.NoDHT = true
	scfg.DisablePEX = true
	scfg.Seed = true // 愿意对其它 peer 上传
	scfg.NoDefaultPortForwarding = true
	seeder, err := torrent.NewClient(scfg)
	if err != nil {
		t.Fatalf("seeder client: %v", err)
	}
	defer seeder.Close()
	st, err := seeder.AddTorrent(mi)
	if err != nil {
		t.Fatalf("seeder add torrent: %v", err)
	}
	<-st.GotInfo()
	// 磁盘上已有完整数据:校验一次让客户端确认所有 piece 都完整,否则
	// 做种端会认为自己什么都没有,不会上传。
	if err := st.VerifyData(); err != nil {
		t.Fatalf("seeder verify data: %v", err)
	}
	// 完成状态由客户端的哈希与事件循环异步推进,轮询等待它稳定下来。
	verifyDeadline := time.Now().Add(15 * time.Second)
	for !st.Complete().Bool() {
		if time.Now().After(verifyDeadline) {
			t.Fatalf("seeder torrent not complete after verify: pieces=%d completed=%d",
				st.NumPieces(), st.BytesCompleted())
		}
		time.Sleep(20 * time.Millisecond)
	}

	peerAddr := ""
	for _, a := range seeder.ListenAddrs() {
		if tcp, ok := a.(*net.TCPAddr); ok {
			peerAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(tcp.Port))
			break
		}
	}
	if peerAddr == "" {
		t.Fatal("seeder has no TCP listen address")
	}

	// ---- 下载端:lgdm 的 Manager ----
	dlDir := t.TempDir()
	m, err := New(Config{
		MaxPeersPerTorrent: 8,
		EnableDHT:          false,
		EnablePEX:          false,
		Seeding:            false,
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer m.Close()

	h, err := m.AddTorrentBytes(context.Background(), torrentBytes, dlDir)
	if err != nil {
		t.Fatalf("AddTorrentBytes: %v", err)
	}
	defer h.Close()
	// 与 scheduler 的启动路径一致:添加后请求下载全部内容(元数据已在时
	// 立即标记分片)。
	h.StartDownloading()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.WaitInfo(ctx); err != nil {
		t.Fatalf("WaitInfo: %v", err)
	}
	if name := h.Name(); name != "hello.bin" {
		t.Errorf("Name() = %q, want hello.bin", name)
	}
	// 直接把做种端地址喂给下载端(等价于 tracker/DHT 找到了这个 peer)。
	h.t.AddPeers([]torrent.PeerInfo{{
		Addr:    torrent.StringAddr(peerAddr),
		Source:  torrent.PeerSourceDirect,
		Trusted: true,
	}})

	var (
		lastSnap  Snapshot
		sawPeers  bool
		sawBytes  bool
		completed bool
	)
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		lastSnap = h.Snapshot()
		if lastSnap.Peers > 0 {
			sawPeers = true
		}
		if lastSnap.Downloaded > 0 {
			sawBytes = true
		}
		if lastSnap.Total > 0 && lastSnap.Complete {
			completed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !completed {
		t.Fatalf("download did not complete: snap=%+v sawPeers=%v sawBytes=%v", lastSnap, sawPeers, sawBytes)
	}
	// 回环传输非常快(几十毫秒),ActivePeers 可能在首次采样之前就已经
	// 因为「双方都完整」而断开,所以 peer 数只做提示,不作为断言。
	t.Logf("observed peers=%v bytes=%v", sawPeers, sawBytes)
	if !sawBytes {
		t.Error("never observed downloaded bytes before completion")
	}
	// 做种端确实上传了数据 → 证明这是一次真实的 P2P 传输,而不是从本地
	// 或其它来源「凭空完成」的。
	seedStats := st.Stats()
	if up := seedStats.BytesWrittenData.Int64(); up <= 0 {
		t.Errorf("seeder uploaded %d bytes, want > 0", up)
	}
	if lastSnap.Total != int64(len(content)) {
		t.Errorf("Total = %d, want %d", lastSnap.Total, len(content))
	}
	if len(lastSnap.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(lastSnap.Files))
	}
	// 单文件种子:文件落在任务目录下的 <种子名>(见 File.Path 的语义)。
	if got := lastSnap.Files[0].Path; got != "hello.bin" {
		t.Errorf("file path = %q, want hello.bin", got)
	}
	if lastSnap.Files[0].Completed != int64(len(content)) {
		t.Errorf("file completed = %d, want %d", lastSnap.Files[0].Completed, len(content))
	}

	got, err := os.ReadFile(filepath.Join(dlDir, "hello.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("downloaded content differs from source (got %d bytes, want %d)", len(got), len(content))
	}
	// 未完成的 .part 文件应当在文件下载完成后被重命名为正式文件名。
	if _, err := os.Stat(filepath.Join(dlDir, "hello.bin.part")); !os.IsNotExist(err) {
		t.Errorf("part file still present after completion: %v", err)
	}
}

// TestAddInfoBytesFromCache 验证「磁力链接抓到元数据后落库、之后从缓存的
// info 字典恢复任务」这条路径:恢复时不需要任何 peer 就能拿到种子名、
// 文件列表与总体积(这正是离线续传依赖的能力)。
func TestAddInfoBytesFromCache(t *testing.T) {
	requireLoopback(t)
	content := fakePayload(256 << 10)
	_, torrentBytes := makeSingleFileTorrent(t, "cached.bin", content, 64<<10)

	infoBytes, err := InfoBytesFromTorrentFile(torrentBytes)
	if err != nil {
		t.Fatalf("InfoBytesFromTorrentFile: %v", err)
	}
	if len(infoBytes) == 0 {
		t.Fatal("empty info bytes")
	}

	m, err := New(Config{MaxPeersPerTorrent: 4, EnableDHT: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	h, err := m.AddInfoBytes(context.Background(), infoBytes, nil, t.TempDir())
	if err != nil {
		t.Fatalf("AddInfoBytes: %v", err)
	}
	defer h.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.WaitInfo(ctx); err != nil {
		t.Fatalf("WaitInfo: %v", err)
	}
	if name := h.Name(); name != "cached.bin" {
		t.Errorf("Name() = %q, want cached.bin", name)
	}
	snap := h.Snapshot()
	if snap.Total != int64(len(content)) {
		t.Errorf("Total = %d, want %d", snap.Total, len(content))
	}
	if len(snap.Files) != 1 || snap.Files[0].Path != "cached.bin" {
		t.Errorf("Files = %+v, want one entry for cached.bin", snap.Files)
	}

	// 非 info 字典(垃圾字节)必须报错,调用方据此回退到原始来源。
	if _, err := m.AddInfoBytes(context.Background(), []byte("garbage"), nil, t.TempDir()); err == nil {
		t.Error("expected error for non-info bytes")
	}
}

// TestManagerDoesNotWriteToCwd 是回归用例:BT 客户端的默认存储必须显式
// 指定为「内存完成状态」的实现,否则 anacrolix 会在进程工作目录里新建
// 一个 .torrent.bolt.db(它默认按目录用 bolt 记录分片完成状态)。
func TestManagerDoesNotWriteToCwd(t *testing.T) {
	requireLoopback(t)
	cwd := t.TempDir()
	t.Chdir(cwd)

	m, err := New(Config{MaxPeersPerTorrent: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("creating a bt client wrote %v into the working directory", names)
	}
}

// TestMagnetBeforeMetadata 是回归用例:磁力链接在没有 peer、元数据还没到
// 的这段时间里,StartDownloading/Snapshot 都不能崩。
//
// 背景:anacrolix 在 info 就绪前不会初始化文件表(Torrent.Files() 会
// 解引用空指针),Torrent.DownloadAll() 也会直接 panic——这是「加一个
// 磁力链接就闪退」的真实故障,必须有测试守住。
func TestMagnetBeforeMetadata(t *testing.T) {
	requireLoopback(t)
	m, err := New(Config{MaxPeersPerTorrent: 4, EnableDHT: false, EnablePEX: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	// 40 位十六进制 infohash 的合法磁力链接,但没有任何 peer 能提供元数据。
	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=No%20Peers"
	h, err := m.AddMagnet(context.Background(), magnet, t.TempDir())
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	// 没有元数据:不得 panic,且进度保持「未知」。
	h.StartDownloading()
	snap := h.Snapshot()
	if snap.Total != 0 || snap.Downloaded != 0 {
		t.Errorf("snapshot before metadata = total %d / downloaded %d, want 0/0", snap.Total, snap.Downloaded)
	}
	if len(snap.Files) != 0 {
		t.Errorf("files before metadata = %+v, want empty", snap.Files)
	}
	if snap.Complete {
		t.Error("torrent without metadata must not report complete")
	}
	// 种子名来自磁力链接的 dn 参数,界面上不会是一串空白。
	if name := h.Name(); name != "No Peers" {
		t.Errorf("Name() = %q, want the dn display name", name)
	}
	// 等元数据(不会到)也必须能被取消:WaitInfo 要认出上下文取消。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := h.WaitInfo(ctx); err == nil {
		t.Error("WaitInfo returned nil although no metadata can arrive")
	}
	// 关闭后再等一下,确保等待元数据的 goroutine 已退出(不 panic / 不泄漏)。
	h.Close()
	time.Sleep(50 * time.Millisecond)
	if got := m.Active(); got != 0 {
		t.Errorf("Active() after close = %d, want 0", got)
	}
}

// TestApplyConfigRestart 覆盖设置热更新的两条路径:构造期参数(端口 /
// DHT / PEX / 连接数上限)在没有活动种子时立即重建客户端;有限速改动不
// 重建;有活动种子时先挂起,等最后一个种子结束再重建。
func TestApplyConfigRestart(t *testing.T) {
	requireLoopback(t)
	content := fakePayload(64 << 10)
	_, data := makeSingleFileTorrent(t, "cfg.bin", content, 32<<10)

	base := Config{ListenPort: 0, MaxPeersPerTorrent: 8, EnableDHT: false, EnablePEX: false}
	m, err := New(base)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()
	first := m.cl
	if first == nil {
		t.Fatal("client not created")
	}

	// 只有限速变化:原地调整,不重建客户端。
	limited := base
	limited.DownloadLimit = 4096
	limited.UploadLimit = 2048
	m.ApplyConfig(limited)
	if m.cl != first {
		t.Error("client was recreated for a limit-only change")
	}
	if got := m.dl.Limit(); got != rate.Limit(4096) {
		t.Errorf("download limiter = %v, want 4096", got)
	}
	if got := m.ul.Limit(); got != rate.Limit(2048) {
		t.Errorf("upload limiter = %v, want 2048", got)
	}

	// 构造期参数变化 + 无活动种子:立即重建。
	bigger := limited
	bigger.MaxPeersPerTorrent = 32
	m.ApplyConfig(bigger)
	if m.cl == first {
		t.Error("client was not recreated after a constructor-only change")
	}
	second := m.cl

	// 有活动种子:延后重建,句柄关闭(最后一个种子结束)后再重建。
	h, err := m.AddTorrentBytes(context.Background(), data, t.TempDir())
	if err != nil {
		t.Fatalf("AddTorrentBytes: %v", err)
	}
	deferred := bigger
	deferred.MaxPeersPerTorrent = 64
	m.ApplyConfig(deferred)
	if m.cl != second {
		t.Error("client must not be recreated while a torrent is active")
	}
	if !m.restartPending {
		t.Error("restartPending not recorded")
	}
	h.Close()
	if m.cl == second {
		t.Error("deferred restart did not happen after the last torrent ended")
	}
	if m.restartPending {
		t.Error("restartPending should be cleared after the restart")
	}
}
