import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Link as RouterLink } from 'react-router-dom'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Card from '@mui/material/Card'
import Chip from '@mui/material/Chip'
import CardActionArea from '@mui/material/CardActionArea'
import CardMedia from '@mui/material/CardMedia'
import CircularProgress from '@mui/material/CircularProgress'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import FormControlLabel from '@mui/material/FormControlLabel'
import Radio from '@mui/material/Radio'
import RadioGroup from '@mui/material/RadioGroup'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { api } from './api'
import type { ComicSummary, NovelSummary } from './types'

/** 异步数据加载 Hook：自动执行、可重载、卸载安全。 */
export function useAsync<T>(loader: () => Promise<T>, deps: unknown[] = []): {
  loading: boolean
  error: string | null
  data: T | null
  reload: () => void
} {
  const [state, setState] = useState<{
    loading: boolean
    error: string | null
    data: T | null
  }>({ loading: true, error: null, data: null })
  const [tick, setTick] = useState(0)
  const loaderRef = useRef(loader)
  loaderRef.current = loader

  useEffect(() => {
    let alive = true
    setState((s) => ({ ...s, loading: true, error: null }))
    loaderRef.current().then(
      (data) => {
        if (alive) setState({ loading: false, error: null, data })
      },
      (err: unknown) => {
        if (alive)
          setState({
            loading: false,
            error: err instanceof Error ? err.message : String(err),
            data: null,
          })
      },
    )
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick])

  const reload = useCallback(() => setTick((t) => t + 1), [])
  return { ...state, reload }
}

export function CenterLoading({ label }: { label?: string }) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 2, py: 10 }}>
      <CircularProgress size={36} thickness={4} />
      {label && (
        <Typography variant="body2" color="text.secondary">
          {label}
        </Typography>
      )}
    </Box>
  )
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <Alert severity="error" action={
        onRetry && (
          <Button color="inherit" size="small" onClick={onRetry}>
            重试
          </Button>
        )
      }
    >
      {message}
    </Alert>
  )
}

export function EmptyState({ icon, text, action }: { icon?: ReactNode; text: string; action?: ReactNode }) {
  return (
    <Box sx={{ textAlign: 'center', py: 10, px: 2 }}>
      {icon && <Box sx={{ mb: 1.5, '& .MuiSvgIcon-root': { fontSize: 52, opacity: 0.45 } }}>{icon}</Box>}
      <Typography color="text.secondary">{text}</Typography>
      {action && <Box sx={{ mt: 2 }}>{action}</Box>}
    </Box>
  )
}

export function SectionTitle({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mt: 1, mb: 1.5 }}>
      <Typography variant="h6" sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        {children}
      </Typography>
      {action}
    </Box>
  )
}

export function ComicCard({ comic, showSource }: { comic: ComicSummary; showSource?: boolean }) {
  return (
    <Card sx={{ height: '100%', transition: 'transform .18s ease, box-shadow .18s ease', '&:hover': { transform: 'translateY(-3px)', boxShadow: 8 } }}>
      <CardActionArea
        component={RouterLink}
        to={`/comic/${encodeURIComponent(comic.comic_id)}`}
        sx={{ display: 'block', height: '100%' }}
      >
        <Box
          sx={{
            position: 'relative',
            paddingTop: '138%',
            overflow: 'hidden',
            bgcolor: 'action.hover',
          }}
        >
          {showSource && comic.source_label ? (
            <Chip
              size="small"
              label={comic.source_label}
              sx={{
                position: 'absolute',
                top: 4,
                left: 4,
                zIndex: 2,
                height: 20,
                fontSize: 11,
                bgcolor: 'rgba(0,0,0,.62)',
                color: '#fff',
                '& .MuiChip-label': { px: 1 },
              }}
            />
          ) : null}
          {comic.cover_url ? (
            <CardMedia
              component="img"
              image={api.url(comic.cover_url.startsWith('http') ? `/api/image-proxy?url=${encodeURIComponent(comic.cover_url)}` : comic.cover_url)}
              alt={comic.title}
              loading="lazy"
              decoding="async"
              sx={{ position: 'absolute', top: 0, left: 0, width: '100%', height: '100%', objectFit: 'cover' }}
            />
          ) : (
            <Box
              sx={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                height: '100%',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: 40,
                opacity: 0.3,
              }}
            >
              📖
            </Box>
          )}
        </Box>
        <Box sx={{ p: 1.25 }}>
          <Typography variant="body2" fontWeight={600} noWrap title={comic.title}>
            {comic.title || comic.comic_id}
          </Typography>
          {comic.author ? (
            <Typography variant="caption" color="text.secondary" noWrap display="block">
              {comic.author}
            </Typography>
          ) : null}
        </Box>
      </CardActionArea>
    </Card>
  )
}

const GRID_COLUMNS = { xs: 'repeat(2, 1fr)', sm: 'repeat(3, 1fr)', md: 'repeat(4, 1fr)', lg: 'repeat(6, 1fr)' }

export function ComicGrid({ items, showSource }: { items: ComicSummary[]; showSource?: boolean }) {
  return (
    <Box sx={{ display: 'grid', gridTemplateColumns: GRID_COLUMNS, gap: 2 }}>
      {items.map((c) => (
        <ComicCard key={`${c.source}-${c.comic_id}`} comic={c} showSource={showSource} />
      ))}
    </Box>
  )
}

export function NovelCard({ novel }: { novel: NovelSummary }) {
  return (
    <Card sx={{ height: '100%', transition: 'transform .18s ease, box-shadow .18s ease', '&:hover': { transform: 'translateY(-3px)', boxShadow: 8 } }}>
      <CardActionArea
        component={RouterLink}
        to={`/novel/${encodeURIComponent(novel.novel_id)}`}
        sx={{ display: 'block', height: '100%' }}
      >
        <Box
          sx={{
            position: 'relative',
            paddingTop: '138%',
            overflow: 'hidden',
            bgcolor: 'action.hover',
          }}
        >
          {novel.cover_url ? (
            <CardMedia
              component="img"
              image={api.url(novel.cover_url.startsWith('http') ? `/api/image-proxy?url=${encodeURIComponent(novel.cover_url)}` : novel.cover_url)}
              alt={novel.title}
              loading="lazy"
              decoding="async"
              sx={{ position: 'absolute', top: 0, left: 0, width: '100%', height: '100%', objectFit: 'cover' }}
            />
          ) : (
            <Box
              sx={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                height: '100%',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: 40,
                opacity: 0.3,
              }}
            >
              📕
            </Box>
          )}
        </Box>
        <Box sx={{ p: 1.25 }}>
          <Typography variant="body2" fontWeight={600} noWrap title={novel.title}>
            {novel.title || novel.novel_id}
          </Typography>
          {novel.author ? (
            <Typography variant="caption" color="text.secondary" noWrap display="block">
              {novel.author}
            </Typography>
          ) : null}
        </Box>
      </CardActionArea>
    </Card>
  )
}

export function NovelGrid({ items }: { items: NovelSummary[] }) {
  return (
    <Box sx={{ display: 'grid', gridTemplateColumns: GRID_COLUMNS, gap: 2 }}>
      {items.map((n) => (
        <NovelCard key={`${n.source}-${n.novel_id}`} novel={n} />
      ))}
    </Box>
  )
}

/** 收藏夹选择项（漫画 / 小说通用）。 */
export interface FavoriteFolder {
  id: string
  name: string
}

/** 收藏夹弹窗确认结果：选择已有收藏夹，或输入新名称直接新建。 */
export type FavoriteFolderChoice =
  | { kind: 'folder'; folderId: string }
  | { kind: 'new'; folderName: string }

/**
 * 可复用的「收藏夹选择弹窗」：打开时拉取收藏夹列表，支持选中已有收藏夹或新建。
 * 具体的新建 / 移动请求由调用方在 onConfirm 内按自身接口完成。
 */
export function FavoriteFolderDialog({
  open,
  title = '选择收藏夹',
  loadFolders,
  submitting = false,
  onClose,
  onConfirm,
}: {
  open: boolean
  title?: string
  loadFolders: () => Promise<FavoriteFolder[]>
  submitting?: boolean
  onClose: () => void
  onConfirm: (choice: FavoriteFolderChoice) => void
}) {
  const [folders, setFolders] = useState<FavoriteFolder[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState('0')
  const [newName, setNewName] = useState('')
  const loaderRef = useRef(loadFolders)
  loaderRef.current = loadFolders

  useEffect(() => {
    if (!open) return
    let alive = true
    setLoading(true)
    setError(null)
    setFolders([])
    setSelected('0')
    setNewName('')
    loaderRef.current().then(
      (list) => {
        if (alive) {
          setFolders(list)
          setLoading(false)
        }
      },
      (err: unknown) => {
        if (alive) {
          setError(err instanceof Error ? err.message : String(err))
          setLoading(false)
        }
      },
    )
    return () => {
      alive = false
    }
  }, [open])

  const canConfirm = newName.trim() !== '' || selected !== ''

  const confirm = () => {
    const name = newName.trim()
    if (name) onConfirm({ kind: 'new', folderName: name })
    else onConfirm({ kind: 'folder', folderId: selected })
  }

  return (
    <Dialog open={open} onClose={submitting ? undefined : onClose} fullWidth maxWidth="xs">
      <DialogTitle>{title}</DialogTitle>
      <DialogContent dividers>
        {loading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
            <CircularProgress size={28} />
          </Box>
        ) : error ? (
          <Alert severity="error">{error}</Alert>
        ) : (
          <>
            <RadioGroup
              value={selected}
              onChange={(e) => {
                setSelected(e.target.value)
                setNewName('')
              }}
            >
              <FormControlLabel value="0" control={<Radio size="small" />} label="默认收藏夹" />
              {folders.map((f) => (
                <FormControlLabel
                  key={f.id}
                  value={f.id}
                  control={<Radio size="small" />}
                  label={f.name || `收藏夹 ${f.id}`}
                />
              ))}
            </RadioGroup>
            <TextField
              label="新建收藏夹"
              placeholder="输入名称后确定将直接新建"
              value={newName}
              onChange={(e) => {
                setNewName(e.target.value)
                setSelected('')
              }}
              fullWidth
              size="small"
              sx={{ mt: 2 }}
            />
          </>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={submitting}>
          取消
        </Button>
        <Button onClick={confirm} variant="contained" disabled={submitting || loading || !!error || !canConfirm}>
          {submitting ? '处理中…' : '确定'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

/**
 * 在线人数小统计：把「获取人数」本身当作心跳，每 60 秒打一次。
 * 页面不可见时暂停心跳（真正的「在线」本就不含后台标签页），
 * 重新可见时立即补一次；网络失败时静默保留上一次数值，不打扰用户。
 */
export function OnlineStat() {
  const [online, setOnline] = useState<number | null>(null)

  useEffect(() => {
    let alive = true
    const beat = async () => {
      try {
        const data = await api.get<{ online: number }>('/api/online')
        if (alive && typeof data?.online === 'number') setOnline(data.online)
      } catch {
        // 忽略：在线人数不影响主流程
      }
    }

    // 页面不可见时停止心跳（真正的「在线」本就不含后台标签页），
    // 重新可见时立刻补一次，避免显示过期数值。
    const tick = () => {
      if (document.visibilityState === 'visible') void beat()
    }

    void beat()
    const timer = window.setInterval(tick, 60_000)
    document.addEventListener('visibilitychange', tick)
    return () => {
      alive = false
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', tick)
    }
  }, [])

  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.75 }}>
      <Box
        sx={{
          width: 7,
          height: 7,
          borderRadius: '50%',
          bgcolor: 'success.main',
          boxShadow: '0 0 6px',
          color: 'success.main',
        }}
      />
      <Typography variant="caption" color="text.secondary">
        {online === null ? '在线人数统计中…' : `当前 ${online} 人在线`}
      </Typography>
    </Box>
  )
}
