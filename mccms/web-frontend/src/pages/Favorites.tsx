// 收藏夹：本地账号收藏（/api/me/*）。支持收藏夹分组、跨站点聚合。
import { useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import IconButton from '@mui/material/IconButton'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Stack from '@mui/material/Stack'
import Tab from '@mui/material/Tab'
import Tabs from '@mui/material/Tabs'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import AddIcon from '@mui/icons-material/Add'
import FavoriteIcon from '@mui/icons-material/Favorite'
import FolderIcon from '@mui/icons-material/Folder'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import { Link as RouterLink } from 'react-router-dom'
import { account, type FavoriteItem } from '../account'
import { CenterLoading, EmptyState, SectionTitle, useAsync } from '../components'
import { useAuth } from '../auth'

const ALL = '__all__'

export default function Favorites() {
  const { user, loading } = useAuth()
  const [group, setGroup] = useState(ALL)
  const [tick, setTick] = useState(0)
  const [dlgOpen, setDlgOpen] = useState(false)
  const [newGroup, setNewGroup] = useState('')
  const [menuFor, setMenuFor] = useState<FavoriteItem | null>(null)
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)

  const reload = () => setTick((t) => t + 1)
  const groupsState = useAsync(
    async () => (user ? (await account.listFolders()).list : []),
    [user?.username, tick],
  )
  const favsState = useAsync(
    async () =>
      user
        ? (await account.listFavorites(group === ALL ? undefined : group)).list
        : [],
    [user?.username, group, tick],
  )

  if (loading) return <CenterLoading />
  if (!user) {
    return (
      <EmptyState
        icon={<FavoriteIcon />}
        text="登录后查看收藏"
        action={
          <Button component={RouterLink} to="/login" variant="contained">
            去登录
          </Button>
        }
      />
    )
  }

  const groups = groupsState.data ?? []
  const favs = favsState.data ?? []

  const remove = async () => {
    if (!menuFor) return
    await account.removeFavorite(menuFor.source, menuFor.comic_id)
    setMenuFor(null)
    reload()
  }

  const moveTo = async (g: string) => {
    if (!menuFor) return
    account.moveFavorite(menuFor, g)
    setMenuFor(null)
    reload()
  }

  const createFolder = async () => {
    const name = newGroup.trim()
    if (!name) return
    await account.createFolder(name)
    setNewGroup('')
    setDlgOpen(false)
    reload()
  }

  const deleteGroup = async (name: string) => {
    await account.deleteFolder(name)
    if (group === name) setGroup(ALL)
    reload()
  }

  return (
    <Box>
      <SectionTitle action={
        <Button size="small" startIcon={<AddIcon />} onClick={() => setDlgOpen(true)}>
          新建收藏夹
        </Button>
      }>
        我的收藏
      </SectionTitle>
      <Tabs value={group} onChange={(_, v) => setGroup(v)} sx={{ mb: 2 }} variant="scrollable" scrollButtons="auto">
        <Tab value={ALL} icon={<FavoriteIcon />} iconPosition="start" label="全部" />
        {groups.map((g) => (
          <Tab key={g.name} value={g.name} icon={<FolderIcon />} iconPosition="start" label={g.name} />
        ))}
      </Tabs>
      {groupsState.error && <Alert severity="error" sx={{ mb: 1 }}>{groupsState.error}</Alert>}
      {favsState.error && <Alert severity="error" sx={{ mb: 1 }}>{favsState.error}</Alert>}
      {group !== ALL && (
        <Stack direction="row" sx={{ mb: 1 }}>
          <Button size="small" color="error" startIcon={<DeleteIcon />} onClick={() => void deleteGroup(group)}>
            删除当前收藏夹（收藏不会被删除）
          </Button>
        </Stack>
      )}

      {groupsState.loading || favsState.loading ? (
        <CenterLoading />
      ) : favs.length === 0 ? (
        <EmptyState icon={<FavoriteIcon />} text={group === ALL ? '还没有收藏' : `「${group}」里还没有收藏`} />
      ) : (
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'repeat(3,1fr)', sm: 'repeat(4,1fr)', md: 'repeat(6,1fr)' }, gap: 1.5 }}>
          {favs.map((f) => (
            <Box key={`${f.source}:${f.comic_id}`} sx={{ position: 'relative' }}>
              <Box
                component={RouterLink}
                to={`/comic/${encodeURIComponent(f.comic_id)}?site=${encodeURIComponent(f.source)}`}
                sx={{ display: 'block', textDecoration: 'none', color: 'inherit' }}
              >
                <Box sx={{ pt: '142%', borderRadius: 2, overflow: 'hidden', bgcolor: 'aura.surfaceContainerHigh', position: 'relative', mb: 0.5 }}>
                  {f.cover_url && (
                    <Box
                      component="img"
                      src={f.cover_url}
                      alt={f.title}
                      loading="lazy"
                      sx={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'cover' }}
                    />
                  )}
                  <Chip size="small" label={f.source} sx={{ position: 'absolute', left: 4, top: 4, fontSize: 11, height: 20 }} />
                </Box>
                <Typography variant="body2" noWrap title={f.title}>{f.title}</Typography>
                {f.folder_id && (
                  <Typography variant="caption" color="text.secondary" noWrap>
                    <FolderIcon sx={{ fontSize: 12, verticalAlign: '-2px', mr: 0.25 }} />
                    {groups.find((g) => g.id === f.folder_id)?.name ?? ''}
                  </Typography>
                )}
              </Box>
              <IconButton
                size="small"
                aria-label="收藏操作"
                onClick={(e) => {
                  setMenuFor(f)
                  setAnchor(e.currentTarget)
                }}
                sx={{ position: 'absolute', right: 2, top: 2, bgcolor: 'rgba(0,0,0,.45)', color: '#fff', '&:hover': { bgcolor: 'rgba(0,0,0,.65)' } }}
              >
                <MoreVertIcon fontSize="small" />
              </IconButton>
            </Box>
          ))}
          <Menu anchorEl={anchor} open={!!menuFor && !!anchor} onClose={() => setMenuFor(null)}>
            <MenuItem disabled>移动到收藏夹</MenuItem>
            {groups.map((g) => (
              <MenuItem
                key={g.name}
                sx={{ pl: 4 }}
                selected={menuFor?.folder_id === g.id}
                onClick={() => g.id && void moveTo(g.id)}
              >
                {g.name}
              </MenuItem>
            ))}
            <MenuItem onClick={() => void remove()} sx={{ color: 'error.main' }}>
              <DeleteIcon fontSize="small" sx={{ mr: 1 }} />
              取消收藏
            </MenuItem>
          </Menu>
        </Box>
      )}

      <Dialog open={dlgOpen} onClose={() => setDlgOpen(false)} maxWidth="xs" fullWidth>
        <DialogTitle>新建收藏夹</DialogTitle>
        <DialogContent>
          <TextField
            autoFocus
            fullWidth
            size="small"
            label="名称"
            value={newGroup}
            onChange={(e) => setNewGroup(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && void createFolder()}
            sx={{ mt: 1 }}
          />
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDlgOpen(false)}>取消</Button>
          <Button variant="contained" onClick={() => void createFolder()} disabled={!newGroup.trim()}>
            创建
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}