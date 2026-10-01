package web

import (
	"net/http"
	"strings"
	"sync"

	"github.com/mccms/mccms-go/internal/mc"
)

// handleHome 首页聚合：免费专区 + 精选推荐。
// 精选推荐把多个来源轮转混合成一条统一推荐流，前端只发一次请求。
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	var (
		free    []comicSummaryDTO
		premium []comicSummaryDTO
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); free = s.freeZoneSummaries() }()
	go func() { defer wg.Done(); premium = s.premiumSummaries() }()
	wg.Wait()

	ok(w, map[string]any{
		"free":    nonNilSummaries(free),
		"premium": nonNilSummaries(premium),
	})
}

func nonNilSummaries(v []comicSummaryDTO) []comicSummaryDTO {
	if v == nil {
		return []comicSummaryDTO{}
	}
	return v
}

func (s *Server) freeComicSetting() string {
	if s.accounts == nil {
		return ""
	}
	v, err := s.accounts.Store().GetSetting(settingVipFreeComics)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// freeZoneSummaries 把免费白名单（"站点:ID" 列表）解析成可展示的卡片。
func (s *Server) freeZoneSummaries() []comicSummaryDTO {
	raw := s.freeComicSetting()
	if raw == "" {
		return nil
	}
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		out []comicSummaryDTO
	)
	seen := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		site, id, found := strings.Cut(item, ":")
		if !found || site == "" || id == "" {
			continue
		}
		seen[item] = true
		wg.Add(1)
		go func(site, id string) {
			defer wg.Done()
			c, err := s.clientOf(site)
			if err != nil {
				return
			}
			comic, err := c.GetComicDetail(id, false)
			if err != nil || comic == nil {
				return
			}
			dto := toSummaryDTO(mc.ComicSummary{
				ID:     comic.ComicID,
				Name:   comic.Name,
				Author: comic.Author(),
				Cover:  comic.Cover,
				Tags:   comic.Tags,
				Site:   site,
			})
			mu.Lock()
			out = append(out, dto)
			mu.Unlock()
		}(site, id)
	}
	wg.Wait()
	return out
}

// premiumSummaries 跨源取最近更新，每个来源取固定条数后轮转交错，
// 这样任何单一来源都不会霸占整屏。
func (s *Server) premiumSummaries() []comicSummaryDTO {
	const perSource = 12

	buckets := make([][]mc.ComicSummary, len(mc.AllSites))
	var wg sync.WaitGroup
	for i, site := range mc.AllSites {
		wg.Add(1)
		go func(idx int, site string) {
			defer wg.Done()
			c, err := s.clientOf(site)
			if err != nil {
				return
			}
			res, err := c.UpdateList(1)
			if err != nil || res == nil {
				return
			}
			items := res.Items
			if len(items) > perSource {
				items = items[:perSource]
			}
			buckets[idx] = items
		}(i, site)
	}
	wg.Wait()

	out := make([]comicSummaryDTO, 0, perSource*len(mc.AllSites))
	idx := make([]int, len(buckets))
	for {
		progressed := false
		for i := range buckets {
			if idx[i] < len(buckets[i]) {
				out = append(out, toSummaryDTO(buckets[i][idx[i]]))
				idx[i]++
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
}
