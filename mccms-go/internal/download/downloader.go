// Package download 负责把章节/漫画的图片抓到本地，并按需还原乱序图。
package download

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mccms/mccms-go/internal/client"
	"github.com/mccms/mccms-go/internal/decode"
	"github.com/mccms/mccms-go/internal/mc"
)

// Options 下载配置。
type Options struct {
	DirRule        *mc.DirRule
	ImageThreads   int
	ChapterThreads int
	Decode         bool // 是否还原乱序图（默认 true）
	Cache          bool // 已存在则跳过
}

// DefaultOptions 默认下载配置。
func DefaultOptions() Options {
	return Options{
		DirRule:        nil,
		ImageThreads:   16,
		ChapterThreads: 4,
		Decode:         true,
		Cache:          true,
	}
}

// Task 是一次下载任务的内部状态（含锁，不可直接拷贝/序列化）。
type Task struct {
	ID          string
	Site        string
	AlbumID     string
	AlbumTitle  string
	ChapterID   string
	ChapterName string
	Status      string // running / done / failed / denied
	Stage       string
	Message     string

	TotalImages      int
	DownloadedImages int
	FailedImages     int
	Percent          float64

	SavePath string
	Error    string

	mu       sync.Mutex
	started  time.Time
	fileList []string
}

// TaskView 是 Task 的只读快照（不含锁，可安全序列化与拷贝）。
type TaskView struct {
	ID          string `json:"task_id"`
	Site        string `json:"site"`
	AlbumID     string `json:"album_id"`
	AlbumTitle  string `json:"album_title"`
	ChapterID   string `json:"chapter_id,omitempty"`
	ChapterName string `json:"chapter_name,omitempty"`
	Status      string `json:"status"`
	Stage       string `json:"stage"`
	Message     string `json:"message"`

	TotalImages      int     `json:"total_images"`
	DownloadedImages int     `json:"downloaded_images"`
	FailedImages     int     `json:"failed_images"`
	Percent          float64 `json:"percent"`

	SavePath string `json:"save_path,omitempty"`
	Error    string `json:"error,omitempty"`
	Files    int    `json:"files"`
}

func (t *Task) setStage(stage, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Stage, t.Message = stage, msg
}

func (t *Task) setTitle(album, chapter string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if album != "" {
		t.AlbumTitle = album
	}
	if chapter != "" {
		t.ChapterName = chapter
	}
}

func (t *Task) setSavePath(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.SavePath = path
}

func (t *Task) addTotal(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.TotalImages += n
	t.recalc()
}

func (t *Task) markDone(ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ok {
		t.DownloadedImages++
	} else {
		t.FailedImages++
	}
	t.recalc()
}

func (t *Task) recalc() {
	if t.TotalImages > 0 {
		t.Percent = float64(t.DownloadedImages) / float64(t.TotalImages) * 100
		if t.Percent > 100 {
			t.Percent = 100
		}
	}
}

func (t *Task) addFile(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fileList = append(t.fileList, path)
}

func (t *Task) finish(status, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
	t.Message = msg
	if status == "done" {
		t.Percent = 100
	}
}

// Snapshot 取一致的只读快照。
func (t *Task) Snapshot() TaskView {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TaskView{
		ID:               t.ID,
		Site:             t.Site,
		AlbumID:          t.AlbumID,
		AlbumTitle:       t.AlbumTitle,
		ChapterID:        t.ChapterID,
		ChapterName:      t.ChapterName,
		Status:           t.Status,
		Stage:            t.Stage,
		Message:          t.Message,
		TotalImages:      t.TotalImages,
		DownloadedImages: t.DownloadedImages,
		FailedImages:     t.FailedImages,
		Percent:          t.Percent,
		SavePath:         t.SavePath,
		Error:            t.Error,
		Files:            len(t.fileList),
	}
}

// Manager 管理后台下载任务。
type Manager struct {
	mu    sync.Mutex
	tasks map[string]*Task
	seq   int
}

// NewManager 创建任务管理器。
func NewManager() *Manager {
	return &Manager{tasks: map[string]*Task{}}
}

// Submit 提交一个后台任务。
func (m *Manager) Submit(site, albumID, albumTitle, chapterID, chapterName string,
	run func(task *Task) error) *Task {

	m.mu.Lock()
	m.seq++
	id := "task-" + mc.IntToStr(m.seq)
	task := &Task{
		ID:          id,
		Site:        site,
		AlbumID:     albumID,
		AlbumTitle:  albumTitle,
		ChapterID:   chapterID,
		ChapterName: chapterName,
		Status:      "running",
		Stage:       "queued",
		Message:     "任务已创建",
		started:     time.Now(),
	}
	m.tasks[id] = task
	m.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				task.finish("failed", "内部错误")
				task.Error = "内部错误"
			}
		}()

		err := run(task)
		if err == nil {
			task.finish("done", "下载完成")
			return
		}

		if mc.IsAccessDenied(err) {
			task.finish("denied", "无权限访问")
		} else {
			task.finish("failed", "下载失败")
		}
		task.Error = err.Error()
	}()

	return task
}

// Get 取任务快照。
func (m *Manager) Get(id string) (TaskView, bool) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return TaskView{}, false
	}
	return t.Snapshot(), true
}

// List 列出全部任务快照（最新在前）。
func (m *Manager) List() []TaskView {
	m.mu.Lock()
	all := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		all = append(all, t)
	}
	m.mu.Unlock()

	out := make([]TaskView, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, all[i].Snapshot())
	}
	return out
}

// ---- 下载实现 ---------------------------------------------------------------

// DownloadChapter 下载单个章节。
func DownloadChapter(c client.Client, chapterID, comicID string, opts Options, task *Task) (*mc.Chapter, error) {
	task.setStage("chapter", "读取章节信息")
	chapter, err := c.GetChapterDetail(chapterID, comicID, false)
	if err != nil {
		return nil, err
	}

	if chapter.FromComic == nil && chapter.ComicID != "" {
		// 补漫画上下文，让 dir_rule 里的 C* 字段可用
		if comic, cerr := c.GetComicDetail(chapter.ComicID, false); cerr == nil {
			chapter.FromComic = comic
		}
	}

	task.setStage("images", "获取图片列表")
	if _, err := c.FetchImageURLs(chapter); err != nil {
		return nil, err
	}
	if len(chapter.ImageURLs) == 0 {
		return nil, mc.Errorf("章节 [%s] 没有返回任何图片", chapterID)
	}

	task.setTitle(chapter.ComicName(), chapter.Name)
	task.addTotal(len(chapter.ImageURLs))

	dir, err := opts.DirRule.ImageDir(chapter.FromComic, chapter)
	if err != nil {
		return nil, err
	}
	task.setSavePath(dir)

	task.setStage("download", "下载图片")
	downloadImages(c, chapter, dir, opts, task)

	snapshot := task.Snapshot()
	if snapshot.FailedImages > 0 {
		return chapter, mc.Errorf("%d/%d 张图片下载失败", snapshot.FailedImages, snapshot.TotalImages)
	}
	return chapter, nil
}

// DownloadComic 下载整本漫画（并发按章节）。
func DownloadComic(c client.Client, comicID string, opts Options, task *Task) (*mc.Comic, error) {
	task.setStage("comic", "读取漫画信息")
	comic, err := c.GetComicDetail(comicID, true)
	if err != nil {
		return nil, err
	}

	task.setTitle(comic.Name, "")

	root, err := opts.DirRule.ComicRoot(comic)
	if err != nil {
		return nil, err
	}
	task.setSavePath(root)

	threads := opts.ChapterThreads
	if threads < 1 {
		threads = 1
	}
	sem := make(chan struct{}, threads)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i := range comic.Chapters {
		chapter, cerr := comic.BuildChapter(i)
		if cerr != nil {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ch *mc.Chapter) {
			defer wg.Done()
			defer func() { <-sem }()

			if _, derr := DownloadChapter(c, ch.ChapterID, comicID, opts, task); derr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = derr
				}
				mu.Unlock()
			}
		}(chapter)
	}
	wg.Wait()

	if firstErr != nil {
		return comic, firstErr
	}
	return comic, nil
}

// downloadImages 并发下载一个章节的所有图片。
func downloadImages(c client.Client, chapter *mc.Chapter, dir string, opts Options, task *Task) {
	threads := opts.ImageThreads
	if threads < 1 {
		threads = 1
	}

	sem := make(chan struct{}, threads)
	var wg sync.WaitGroup

	for idx := range chapter.ImageURLs {
		img, err := chapter.ImageAt(idx)
		if err != nil {
			task.markDone(false)
			continue
		}

		savePath := filepath.Join(dir, mc.FixWinDirName(img.FileName)+img.Suffix)
		img.SavePath = savePath

		if opts.Cache {
			if st, serr := os.Stat(savePath); serr == nil && st.Size() > 0 {
				task.markDone(true)
				task.addFile(savePath)
				continue
			}
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(im *mc.Image) {
			defer wg.Done()
			defer func() { <-sem }()

			if err := downloadOne(c, im, opts); err != nil {
				task.markDone(false)
				return
			}
			task.markDone(true)
			task.addFile(im.SavePath)
		}(img)
	}

	wg.Wait()
}

func downloadOne(c client.Client, img *mc.Image, opts Options) error {
	target := img.URL

	// 有些站点列表里给的是「图片页」而不是图片直链（例如 E-Hentai），
	// 需要先解析一次；客户端未实现该能力时保持原样。
	if resolver, okk := c.(client.ImageResolver); okk {
		resolved, rerr := resolver.ResolveImage(img.ChapterID, img.FileName+img.Suffix)
		if rerr != nil {
			return rerr
		}
		if resolved != "" {
			target = resolved
			// 用真实后缀落盘，避免出现「.jpg 里装的是 PNG」这种误导性文件名
			if ext := realExt(resolved); ext != "" && !strings.EqualFold(ext, img.Suffix) {
				img.SavePath = strings.TrimSuffix(img.SavePath, img.Suffix) + ext
			}
		}
	}

	resp, err := c.Postman().Get(target)
	if err != nil {
		return err
	}
	if !resp.OK() {
		return mc.Errorf("图片请求失败 http %d: %s", resp.StatusCode, target)
	}

	data := resp.Body

	// 还原竖带倒序
	if opts.Decode && img.IsScrambled() {
		if decoded, _, derr := decode.DecodeImageBytes(data, img.ScrambleN); derr == nil {
			data = decoded
		}
	}

	if err := mc.EnsureDir(filepath.Dir(img.SavePath)); err != nil {
		return err
	}
	return os.WriteFile(img.SavePath, data, 0o644)
}

// realExt 取 URL 路径里的扩展名（不含 query）。
func realExt(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	ext := filepath.Ext(u)
	if len(ext) > 6 {
		return ""
	}
	return ext
}

// SafeJoin 防止路径穿越（结果必须落在 base 内）。
func SafeJoin(base, name string) string {
	cleaned := filepath.Clean(strings.TrimPrefix(name, "/"))
	return filepath.Join(base, cleaned)
}
