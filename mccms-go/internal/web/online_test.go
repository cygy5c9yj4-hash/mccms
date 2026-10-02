package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOnlineTrackerCountsDistinctVisitors(t *testing.T) {
	tr := newOnlineTracker()
	now := time.Now()

	if got := tr.touch("a:1", now); got != 1 {
		t.Fatalf("首个访客应为 1，得到 %d", got)
	}
	if got := tr.touch("a:2", now); got != 2 {
		t.Fatalf("两个访客应为 2，得到 %d", got)
	}
	// 同一访客重复心跳不应增加人数。
	if got := tr.touch("a:1", now.Add(time.Second)); got != 2 {
		t.Fatalf("重复心跳后仍应为 2，得到 %d", got)
	}
}

func TestOnlineTrackerExpiresAfterTTL(t *testing.T) {
	tr := newOnlineTracker()
	now := time.Now()

	tr.touch("a:1", now)
	tr.touch("a:2", now.Add(time.Minute))

	// 超过在线窗口后，只有仍活跃的访客被计数。
	got := tr.touch("a:2", now.Add(onlineTTL+2*time.Minute))
	if got != 1 {
		t.Fatalf("过期后应只剩 1 人，得到 %d", got)
	}
}

func TestOnlineKeyAnonymousStableAndHashed(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/online", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("User-Agent", "UA-1")

	k1 := s.onlineKey(req)
	k2 := s.onlineKey(req)
	if k1 != k2 {
		t.Fatalf("同一访客的键应稳定: %q vs %q", k1, k2)
	}
	if len(k1) == 0 || k1[:2] != "a:" {
		t.Fatalf("匿名访客键应以 a: 开头，得到 %q", k1)
	}
	if k1 == "a:"+req.RemoteAddr {
		t.Fatalf("不应保存明文 IP，得到 %q", k1)
	}

	// 不同 UA 视为不同访客。
	other := httptest.NewRequest(http.MethodGet, "/api/online", nil)
	other.RemoteAddr = req.RemoteAddr
	other.Header.Set("User-Agent", "UA-2")
	if s.onlineKey(other) == k1 {
		t.Fatal("不同 User-Agent 应产生不同访客键")
	}
}

func TestHandleOnlineReturnsEnvelope(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/online", nil)
	req.RemoteAddr = "203.0.113.10:5555"
	rec := httptest.NewRecorder()
	s.handleOnline(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，得到 %d", rec.Code)
	}

	var body struct {
		St   int `json:"st"`
		Data struct {
			Online int `json:"online"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	if body.St != stOK {
		t.Fatalf("状态码应为 %d，得到 %d", stOK, body.St)
	}
	if body.Data.Online != 1 {
		t.Fatalf("首个心跳应返回在线 1 人，得到 %d", body.Data.Online)
	}
}

// 新访客必须立即可见，而老访客的密集心跳复用缓存，不重复全量扫描。
func TestOnlineTrackerCountCache(t *testing.T) {
	tr := newOnlineTracker()
	now := time.Now()

	tr.touch("a:1", now)
	if got := tr.touch("a:2", now.Add(10*time.Millisecond)); got != 2 {
		t.Fatalf("新访客应立即可见，得到 %d", got)
	}
	if got := tr.touch("a:1", now.Add(20*time.Millisecond)); got != 2 {
		t.Fatalf("缓存窗口内应返回 2，得到 %d", got)
	}
}

// 同一访客连续两次心跳不应被算成两个人。
func TestHandleOnlineDeduplicatesSameClient(t *testing.T) {
	s := newTestServer(t)

	beat := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/online", nil)
		req.RemoteAddr = "203.0.113.77:4444"
		req.Header.Set("User-Agent", "UA-X")
		rec := httptest.NewRecorder()
		s.handleOnline(rec, req)
		var body struct {
			St   int `json:"st"`
			Data struct {
				Online int `json:"online"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
		}
		return body.Data.Online
	}

	if got := beat(); got != 1 {
		t.Fatalf("首次心跳应为 1，得到 %d", got)
	}
	if got := beat(); got != 1 {
		t.Fatalf("同一访客再次心跳仍应为 1，得到 %d", got)
	}
}
