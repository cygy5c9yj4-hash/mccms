package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// onlineTTL 是「在线」的判定窗口：最后一次心跳落在此窗口内即计为在线。
// 前端约每 45 秒心跳一次，因此窗口取 5 分钟，既能容忍偶尔丢包，也不会把
// 关掉页面的人长期算作在线。
const onlineTTL = 5 * time.Minute

// onlineTracker 在内存里记录访客的最后活跃时间，用于统计在线人数。
//
// 访客身份：已登录按用户 ID；匿名按 IP + User-Agent 的哈希。
// 出于隐私考虑不保存明文 IP，只保存哈希后的短标识。
// 进程重启后统计清零——这只是展示用的近似值，不需要持久化。
type onlineTracker struct {
	mu        sync.Mutex
	seen      map[string]time.Time
	lastPurge time.Time

	// 计数缓存：把 O(访客数) 的扫描最多降到每秒一次，
	// 心跳再密集也不会放大成本（重活留在后端但只做一次）。
	countedAt time.Time
	counted   int
}

func newOnlineTracker() *onlineTracker {
	return &onlineTracker{seen: map[string]time.Time{}}
}

// touch 记录一次活跃，并返回当前在线人数。
func (t *onlineTracker) touch(key string, now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	_, existed := t.seen[key]
	t.seen[key] = now

	// 老访客在 1 秒内重复心跳，直接复用缓存结果。
	if existed && now.Sub(t.countedAt) < time.Second {
		return t.counted
	}

	// 清理过期条目：最多每分钟做一次，避免每次心跳都全量扫描。
	if now.Sub(t.lastPurge) >= time.Minute {
		for k, ts := range t.seen {
			if now.Sub(ts) > onlineTTL {
				delete(t.seen, k)
			}
		}
		t.lastPurge = now
	}

	n := 0
	for _, ts := range t.seen {
		if now.Sub(ts) <= onlineTTL {
			n++
		}
	}
	t.countedAt = now
	t.counted = n
	return n
}

// onlineKey 为访问者生成稳定的匿名标识。
// 已登录用户按账号计（同一人换设备只算一个）；匿名访客按 IP + UA 计。
func (s *Server) onlineKey(r *http.Request) string {
	if u, err := s.currentUser(r); err == nil && u != nil {
		return "u:" + u.ID
	}
	sum := sha256.Sum256([]byte(clientIP(r) + "\n" + r.UserAgent()))
	return "a:" + hex.EncodeToString(sum[:8])
}

// handleOnline 既是一次在线心跳，也返回当前在线人数。
// 无需登录，页脚对所有人展示。
func (s *Server) handleOnline(w http.ResponseWriter, r *http.Request) {
	n := s.online.touch(s.onlineKey(r), time.Now())
	ok(w, map[string]any{"online": n})
}
