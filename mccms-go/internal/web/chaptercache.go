package web

import (
	"sync"

	"github.com/mccms/mccms-go/internal/client"
	"github.com/mccms/mccms-go/internal/mc"
)

// 章节图片名 -> 真实 URL 的缓存。
//
// 前端的图片地址形式是 /api/chapter_image/{chapterId}/{name}，
// 而站点给的是完整 URL，因此需要按章节缓存一份「文件名 -> URL」的映射。
// 缓存上限做了简单裁剪，避免长时间运行把内存吃满。

const maxCachedChapters = 64

type chapterImageEntry struct {
	urls      map[string]string
	scrambleN int
}

type chapterImageCache struct {
	mu      sync.Mutex
	entries map[string]chapterImageEntry
	order   []string
}

func newChapterImageCache() *chapterImageCache {
	return &chapterImageCache{entries: map[string]chapterImageEntry{}}
}

func (c *chapterImageCache) put(chapterID string, entry chapterImageEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[chapterID]; !exists {
		c.order = append(c.order, chapterID)
	}
	c.entries[chapterID] = entry

	for len(c.order) > maxCachedChapters {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

func (c *chapterImageCache) get(chapterID string) (chapterImageEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, okk := c.entries[chapterID]
	return e, okk
}

// rememberChapterImages 记录章节的图片列表。
func (s *Server) rememberChapterImages(ch *mc.Chapter) {
	if s.chapterCache == nil || ch == nil {
		return
	}
	urls := make(map[string]string, len(ch.ImageURLs))
	for _, u := range ch.ImageURLs {
		urls[fileNameOf(u)] = u
	}
	s.chapterCache.put(ch.ChapterID, chapterImageEntry{urls: urls, scrambleN: ch.ScrambleN})
}

// lookupChapterImage 把图片文件名解析成真实 URL。
func (s *Server) lookupChapterImage(c client.Client, site, chapterID, name string) (string, int) {
	// 客户端若支持按需解析（如 E-Hentai 列表里给的是「图片页」而不是直链），
	// 必须**先**问它——缓存在章节列表阶段存下的可能正是图片页地址。
	// 它自带缓存，只解析需要的那一张。
	if resolver, okk := c.(client.ImageResolver); okk {
		if u, rerr := resolver.ResolveImage(chapterID, name); rerr == nil && u != "" {
			if s.chapterCache != nil {
				s.chapterCache.put(chapterID, chapterImageEntry{
					urls: map[string]string{name: u},
				})
			}
			return u, 0
		}
	}

	if s.chapterCache != nil {
		if e, okk := s.chapterCache.get(chapterID); okk {
			if u, hit := e.urls[name]; hit {
				return u, e.scrambleN
			}
		}
	}

	// 未命中：重新拉一次章节（顺带填充缓存）
	chapter, err := c.GetChapterDetail(chapterID, "", true)
	if err != nil {
		return "", 0
	}
	s.rememberChapterImages(chapter)

	for _, u := range chapter.ImageURLs {
		if fileNameOf(u) == name {
			return u, chapter.ScrambleN
		}
	}
	return "", 0
}
