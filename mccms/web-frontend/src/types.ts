// 与 Go 后端 v2.go / content.go / aura.go 响应结构对齐的共享类型。

export interface ComicSummary {
  source: string
  /** 匿名来源显示名，如「来源1」；后端下发，避免用户看到真实站点。 */
  source_label?: string
  comic_id: string
  title: string
  author?: string | null
  cover_url?: string | null
  tags: string[]
  category?: string | null
}

export interface ChapterSummary {
  id: string
  title: string
  order: number
}

export interface ComicDetail extends ComicSummary {
  description?: string | null
  is_favorite?: boolean
  chapters: ChapterSummary[]
}

export interface ChapterPage {
  name?: string | null
  url?: string | null
}

export interface ChapterRaw {
  photo_id?: string
  album_id?: string
  scramble_id?: string
  data_original_domain?: string | null
  images?: string[]
  title?: string
  index?: number
}

export interface ChapterDetail {
  source: string
  chapter_id: string
  title?: string | null
  images: ChapterPage[]
  raw: ChapterRaw
}

// ── 小说 ──

export interface NovelSummary {
  source: string
  novel_id: string
  title: string
  author?: string | null
  cover_url?: string | null
  category?: string | null
}

export interface NovelChapterSummary {
  id: string
  title: string
  order: number
}

export interface NovelDetail {
  source: string
  novel_id: string
  title: string
  author?: string | null
  cover_url?: string | null
  description?: string | null
  tags: string[]
  category?: string | null
  is_favorite?: boolean | null
  chapters: NovelChapterSummary[]
}

export interface NovelChapterDetail {
  source: string
  chapter_id: string
  title?: string | null
  content: string
}

/** 小说选话导出任务（后端 novel_dl.go toPublic 对齐）。 */
export interface NovelExportTask {
  task_id: string
  novel_id: string
  novel_title: string
  author?: string | null
  lang: string
  format: string
  status: string
  stage: string
  message: string
  total_chapters: number
  downloaded_chapters: number
  failed_chapters: number
  percent: number
  download_url?: string
}

/** 漫画选话下载任务（后端 dl.go toPublic 对齐）。 */
export interface ComicDownloadTask {
  task_id: string
  album_id: string
  album_title: string
  status: string
  stage: string
  message: string
  total_images: number
  downloaded_images: number
  total_zip_files: number
  zipped_files: number
  percent: number
  download_url?: string
}

export interface SiteMe {
  username: string
  is_admin: boolean
}

// ── 阅读笔记（app/recommend.go 响应结构对齐） ──

/** 作品类型：后端自动识别，决定封面目录与详情页跳转。 */
export type WorkKind = 'comic' | 'novel'

export interface Recommendation {
  id: string
  comic_id: string
  comic_title: string
  kind: WorkKind
  body: string
  author: string
  author_name: string
  created_at: number
  cover_url: string
  can_delete: boolean
}

export interface RecommendationList {
  items: Recommendation[]
  total: number
  page: number
  page_size: number
  has_more: boolean
  is_moderator: boolean
}

export interface V2Comment {
  CID?: number | string
  nickname?: string
  username?: string
  content?: string
  created_at?: string
  likes?: number | string
  spoiler?: string
  replys?: V2Comment[]
  children?: V2Comment[]
  [key: string]: unknown
}

export interface V2UserProfile {
  source: string
  username?: string | null
  nickname?: string | null
  avatar_url?: string | null
  signature?: string | null
  raw?: Record<string, unknown>
}

/** 图片代理地址（同源，供 DescrambledImage 以 crossOrigin 加载）。
 *  domain 参数对齐旧版 Vue 实现，让后端优先用 chapter_view_template 返回的域名拉图。 */
export function chapterImageUrl(photoId: string, imageName: string, domain?: string | null): string {
  let url = `/api/chapter_image/${encodeURIComponent(photoId)}/${encodeURIComponent(imageName)}`
  if (domain) {
    url += `?domain=${encodeURIComponent(domain)}`
  }
  return url
}
