import { useMemo, useState } from 'react'
import { Link as RouterLink, useNavigate } from 'react-router-dom'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Card from '@mui/material/Card'
import Chip from '@mui/material/Chip'
import InputAdornment from '@mui/material/InputAdornment'
import Paper from '@mui/material/Paper'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import ExploreIcon from '@mui/icons-material/Explore'
import SearchIcon from '@mui/icons-material/Search'
import SwipeVerticalIcon from '@mui/icons-material/SwipeVertical'
import WhatshotIcon from '@mui/icons-material/Whatshot'
import { api } from '../api'
import { CenterLoading, ComicCard, ComicGrid, ErrorState, SectionTitle, useAsync } from '../components'
import { useSiteConfig } from '../siteConfig'
import type { ComicSummary } from '../types'

/**
 * 原始列表（/api/latest 等 legacy 裸响应）→ ComicSummary。
 * Latest 页仍在使用，务必保留此导出。
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
      source_label: typeof it.source_label === 'string' ? it.source_label : undefined,
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

interface HomeFeed {
  free?: ComicSummary[]
  premium?: ComicSummary[]
}

/** 首页顶部：聚合搜索入口（不再暴露站点切换）。 */
function Hero() {
  const navigate = useNavigate()
  const { name } = useSiteConfig()
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
        {name}
      </Typography>
      <Typography variant="h4" sx={{ mt: 0.5, mb: 0.5 }}>
        今天想看点什么？
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2.5 }}>
        精选内容已为你聚合好，直接搜索或浏览即可。
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
  const feed = useAsync(() => api.get<HomeFeed>('/api/home'), [])

  const free = useMemo(() => feed.data?.free ?? [], [feed.data])
  const premium = useMemo(() => feed.data?.premium ?? [], [feed.data])

  return (
    <Box>
      <Hero />

      {feed.loading ? (
        <CenterLoading />
      ) : feed.error ? (
        <ErrorState message={`内容加载失败：${feed.error}`} onRetry={feed.reload} />
      ) : (
        <>
          {free.length > 0 ? (
            <Box sx={{ mb: 4 }}>
              <SectionTitle>免费专区</SectionTitle>
              <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
                无需会员即可直接阅读
              </Typography>
              <CoverRow items={free} />
            </Box>
          ) : null}

          <Box>
            <SectionTitle>精选推荐</SectionTitle>
            {premium.length === 0 ? (
              <Card sx={{ p: 4, textAlign: 'center', borderRadius: 3 }}>
                <Typography color="text.secondary">暂时没有拿到内容，稍后再试。</Typography>
              </Card>
            ) : (
              <ComicGrid items={premium} />
            )}
          </Box>
        </>
      )}
    </Box>
  )
}
