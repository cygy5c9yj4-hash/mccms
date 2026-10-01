import { useMemo, useState } from 'react'
import { Link as RouterLink, useNavigate } from 'react-router-dom'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Card from '@mui/material/Card'
import Chip from '@mui/material/Chip'
import InputAdornment from '@mui/material/InputAdornment'
import MenuItem from '@mui/material/MenuItem'
import Paper from '@mui/material/Paper'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import ExploreIcon from '@mui/icons-material/Explore'
import SearchIcon from '@mui/icons-material/Search'
import SwipeVerticalIcon from '@mui/icons-material/SwipeVertical'
import WhatshotIcon from '@mui/icons-material/Whatshot'
import { api } from '../api'
import { CenterLoading, ComicCard, ErrorState, SectionTitle, useAsync } from '../components'
import type { ComicSummary } from '../types'
import { useSite } from '../site'

/**
 * 原始列表（/api/latest、/api/promote 的分区内容）→ ComicSummary。
 *
 * 后端返回的 image 都是绝对地址；若碰到相对路径，只补当前源，
 * 不再像上游那样拼 JM 的 CDN 域名（那对本项目是错误域名）。
 */
export function rawListToSummaries(data: unknown): ComicSummary[] {
  const list = Array.isArray(data) ? data : []
  const out: ComicSummary[] = []
  for (const item of list) {
    if (typeof item !== 'object' || item === null) continue
    const it = item as Record<string, unknown>
    const id = it.id ?? it.comic_id ?? it.album_id
    if (id === undefined || id === null || String(id) === '') continue

    const authorRaw = it.author
    const rawImage = it.image ?? it.cover ?? it.cover_url
    let coverUrl: string | null = null
    if (typeof rawImage === 'string' && rawImage !== '') {
      coverUrl = rawImage.startsWith('http')
        ? rawImage
        : rawImage.startsWith('/')
          ? `/api/image-proxy?url=${encodeURIComponent(rawImage)}`
          : null
    }

    out.push({
      source: String(it.source ?? ''),
      comic_id: String(id),
      title: String(it.name ?? it.title ?? ''),
      author: Array.isArray(authorRaw)
        ? authorRaw.map(String).join(', ')
        : authorRaw
          ? String(authorRaw)
          : null,
      cover_url: coverUrl,
      tags: Array.isArray(it.tags) ? (it.tags as unknown[]).map(String) : [],
    })
  }
  return out
}

/** 官方推荐里的条目 → 小说摘要。本项目没有小说源，恒返回空数组。 */
export function novelListFromPromote(_data: unknown): never[] {
  return []
}

interface PromoteSection {
  id?: number | string
  title?: string
  slug?: string
  type?: string
  content?: unknown[]
}

function normalizeSections(data: unknown): PromoteSection[] {
  if (Array.isArray(data)) {
    return data.filter((s) => s && typeof s === 'object') as PromoteSection[]
  }
  return []
}

async function loadPromote(): Promise<PromoteSection[]> {
  const d = await api.get<unknown>('/api/promote')
  return normalizeSections(d)
}

/** 首页顶部：站点切换 + 搜索 */
function Hero() {
  const navigate = useNavigate()
  const { site, sites, current, setSite } = useSite()
  const [q, setQ] = useState('')

  const submit = () => {
    const kw = q.trim()
    if (!kw) return
    navigate(`/search?q=${encodeURIComponent(kw)}`)
  }

  return (
    <Paper
      elevation={0}
      sx={{
        position: 'relative',
        overflow: 'hidden',
        borderRadius: 4,
        p: { xs: 2.5, sm: 4 },
        mb: 4,
        border: '1px solid',
        borderColor: 'divider',
        background: (t) =>
          t.palette.mode === 'dark'
            ? 'linear-gradient(135deg, rgba(255,177,200,.16) 0%, rgba(194,67,111,.10) 45%, transparent 100%)'
            : 'linear-gradient(135deg, rgba(168,59,99,.14) 0%, rgba(255,217,226,.55) 45%, transparent 100%)',
      }}
    >
      <Typography variant="overline" color="primary">
        {current?.name ?? site} · {current?.domains?.[0] ?? ''}
      </Typography>
      <Typography variant="h4" sx={{ mt: 0.5, mb: 0.5 }}>
        今天想看点什么？
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2.5 }}>
        支持多个站点，切换后全站生效。搜索、榜单、分类、阅读与下载都走同一套后端。
      </Typography>

      <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap', alignItems: 'center' }}>
        <TextField
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') submit()
          }}
          placeholder="搜索作品名 / 作者 / 标签"
          size="small"
          sx={{ flex: '1 1 260px', maxWidth: 460, bgcolor: 'background.paper', borderRadius: 1 }}
          InputProps={{
            startAdornment: (
              <InputAdornment position="start">
                <SearchIcon fontSize="small" />
              </InputAdornment>
            ),
          }}
        />
        <Button variant="contained" onClick={submit} sx={{ height: 40 }}>
          搜索
        </Button>

        {sites.length > 1 && (
          <TextField
            select
            size="small"
            label="站点"
            value={site}
            onChange={(e) => setSite(e.target.value)}
            sx={{ minWidth: 150, bgcolor: 'background.paper', borderRadius: 1 }}
          >
            {sites.map((s) => (
              <MenuItem key={s.key} value={s.key}>
                {s.name}
              </MenuItem>
            ))}
          </TextField>
        )}
      </Box>

      <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap', mt: 2 }}>
        <Chip
          icon={<WhatshotIcon />}
          label="排行榜"
          component={RouterLink}
          to="/leaderboard"
          clickable
          variant="outlined"
        />
        <Chip
          icon={<SwipeVerticalIcon />}
          label="最近更新"
          component={RouterLink}
          to="/latest"
          clickable
          variant="outlined"
        />
        <Chip
          icon={<ExploreIcon />}
          label="分类浏览"
          component={RouterLink}
          to="/categories"
          clickable
          variant="outlined"
        />
      </Box>
    </Paper>
  )
}

/** 横向封面行：scroll-snap + 隐藏滚动条 */
function CoverRow({ items }: { items: ComicSummary[] }) {
  if (items.length === 0) return null
  return (
    <Box
      sx={{
        display: 'flex',
        gap: 1.5,
        overflowX: 'auto',
        overscrollBehaviorX: 'contain',
        scrollSnapType: 'x mandatory',
        WebkitOverflowScrolling: 'touch',
        scrollbarWidth: 'none',
        msOverflowStyle: 'none',
        '&::-webkit-scrollbar': { display: 'none' },
        mx: { xs: -2, sm: -3 },
        px: { xs: 2, sm: 3 },
        pb: 1,
      }}
    >
      {items.map((c) => (
        <Box
          key={`${c.source}-${c.comic_id}`}
          sx={{ flex: '0 0 auto', width: { xs: 108, sm: 128 }, scrollSnapAlign: 'start' }}
        >
          <ComicCard comic={c} />
        </Box>
      ))}
    </Box>
  )
}

export default function Home() {
  const promote = useAsync(loadPromote, [])

  const sections = useMemo(() => {
    const raw = promote.data ?? []
    return raw
      .map((section, i) => ({
        key: String(section.id ?? section.slug ?? i),
        title: section.title || '推荐',
        items: rawListToSummaries(section.content),
      }))
      .filter((s) => s.items.length > 0)
  }, [promote.data])

  return (
    <Box>
      <Hero />

      {promote.loading ? (
        <CenterLoading />
      ) : promote.error ? (
        <ErrorState message={`内容加载失败：${promote.error}`} onRetry={promote.reload} />
      ) : sections.length === 0 ? (
        <Card sx={{ p: 4, textAlign: 'center', borderRadius: 3 }}>
          <Typography color="text.secondary">暂时没有拿到内容，稍后再试或换个站点看看。</Typography>
        </Card>
      ) : (
        sections.map((s) => (
          <Box key={s.key} sx={{ mb: 3.5 }}>
            <SectionTitle>{s.title}</SectionTitle>
            <CoverRow items={s.items} />
          </Box>
        ))
      )}
    </Box>
  )
}
