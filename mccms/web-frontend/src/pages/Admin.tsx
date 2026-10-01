// 管理员后台：用户管理 + 站点总览 + 审计日志。
import { useEffect, useState } from 'react'
import Alert from '@mui/material/Alert'
import Avatar from '@mui/material/Avatar'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogContentText from '@mui/material/DialogContentText'
import DialogTitle from '@mui/material/DialogTitle'
import Divider from '@mui/material/Divider'
import IconButton from '@mui/material/IconButton'
import ListItemIcon from '@mui/material/ListItemIcon'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Stack from '@mui/material/Stack'
import Switch from '@mui/material/Switch'
import Tab from '@mui/material/Tab'
import Tabs from '@mui/material/Tabs'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import AdminPanelSettingsIcon from '@mui/icons-material/AdminPanelSettings'
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline'
import KeyIcon from '@mui/icons-material/Key'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import StarIcon from '@mui/icons-material/Star'
import { Link as RouterLink } from 'react-router-dom'
import { account, type PublicUser } from '../account'
import { CenterLoading, EmptyState, SectionTitle, useAsync } from '../components'
import { useAuth } from '../auth'
import AdminVip from './AdminVip'
import AdminSite from './AdminSite'

export default function Admin() {
  const { user, loading } = useAuth()
  const [tab, setTab] = useState(0)
  const [tick, setTick] = useState(0)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')

  const overview = useAsync(async () => (user?.is_admin ? account.admin.overview() : null), [user?.username, tick])
  const users = useAsync(async () => (user?.is_admin ? (await account.admin.listUsers()).list : []), [user?.username, tick])
  const audit = useAsync(async () => (user?.is_admin ? (await account.admin.audit()).list : []), [user?.username, tick])
  const reg = useAsync(async () => (user?.is_admin ? account.admin.settings() : null), [user?.username, tick])
  const reload = () => setTick((t) => t + 1)

  if (loading) return <CenterLoading />
  if (!user) {
    return (
      <EmptyState
        icon={<AdminPanelSettingsIcon />}
        text="请先登录"
        action={<Button component={RouterLink} to="/login" variant="contained">去登录</Button>}
      />
    )
  }
  if (!user.is_admin) return <EmptyState icon={<AdminPanelSettingsIcon />} text="仅管理员可访问" />

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    setErr('')
    setMsg('')
    try {
      await fn()
      setMsg(ok)
      reload()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  // 管理面板不该被搜索引擎收录，也不随站点一起缓存到用户端。
  useEffect(() => {
    const meta = document.createElement('meta')
    meta.name = 'robots'
    meta.content = 'noindex, nofollow'
    document.head.appendChild(meta)
    return () => {
      document.head.removeChild(meta)
    }
  }, [])

  return (
    <Box>
      <SectionTitle>管理后台</SectionTitle>
      {err && <Alert severity="error" sx={{ mb: 1 }} onClose={() => setErr('')}>{err}</Alert>}
      {msg && <Alert severity="success" sx={{ mb: 1 }} onClose={() => setMsg('')}>{msg}</Alert>}
      <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mb: 2 }}>
        <Tab label="总览" />
        <Tab label={`用户 (${users.data?.length ?? '…'})`} />
        <Tab label="审计日志" />
        <Tab label="VIP / 卡密" />
        <Tab label="站点设置" />
      </Tabs>

      <Box hidden={tab !== 0}>
        {overview.loading ? (
          <CenterLoading />
        ) : (
          <>
            <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} sx={{ mb: 3 }}>
              <Stat label="用户总数" value={overview.data?.counts.users} />
              <Stat label="管理员" value={overview.data?.counts.admins} />
              <Stat label="已禁用" value={overview.data?.counts.disabled_users} />
              <Stat label="活跃会话" value={overview.data?.counts.sessions} />
            </Stack>
            <Divider sx={{ mb: 2 }} />
            <Stack direction="row" alignItems="center" spacing={1}>
              <Switch
                checked={reg.data?.registration_open ?? true}
                onChange={(e) => void run(() => account.admin.setRegistrationOpen(e.target.checked), '已保存')}
              />
              <Typography variant="body2">开放注册</Typography>
            </Stack>
          </>
        )}
      </Box>

      <Box hidden={tab !== 1}>
        <UserTable users={users.data ?? []} me={user.username} onChanged={reload} onError={setErr} />
      </Box>

      <Box hidden={tab !== 2}>
        {audit.loading ? (
          <CenterLoading />
        ) : (audit.data ?? []).length === 0 ? (
          <EmptyState text="暂无审计记录" />
        ) : (
          <Stack spacing={0.5}>
            {(audit.data ?? []).map((a, i) => (
              <Box key={i} sx={{ display: 'flex', gap: 1, alignItems: 'baseline', fontSize: 13 }}>
                <Box component="span" sx={{ color: 'text.secondary', whiteSpace: 'nowrap' }}>{a.at?.slice(0,19).replace('T',' ')}</Box>
                <Box component="span" sx={{ fontWeight: 600 }}>{a.actor_name}</Box>
                <Box component="span">{a.action}</Box>
                {a.detail && <Box component="span" sx={{ color: 'text.secondary' }}>{a.target ? `${a.target} · ${a.detail}` : a.detail}</Box>}
              </Box>
            ))}
          </Stack>
        )}
      </Box>

      <Box hidden={tab !== 3}>
        <AdminVip />
      </Box>

      <Box hidden={tab !== 4}>
        <AdminSite />
      </Box>
    </Box>
  )
}

function Stat({ label, value }: { label: string; value: number | undefined }) {
  return (
    <Box sx={{ flex: 1, bgcolor: 'aura.surfaceContainer', borderRadius: 3, p: 2 }}>
      <Typography variant="caption" color="text.secondary">{label}</Typography>
      <Typography variant="h5" fontWeight={700}>{value ?? '—'}</Typography>
    </Box>
  )
}

function UserTable({
  users,
  me,
  onChanged,
  onError,
}: {
  users: PublicUser[]
  me: string
  onChanged: () => void
  onError: (m: string) => void
}) {
  const [menuFor, setMenuFor] = useState<PublicUser | null>(null)
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const [dlg, setDlg] = useState<'delete' | 'password' | 'tier' | 'role' | null>(null)
  const [pw, setPw] = useState('')
  const [tier, setTier] = useState('vip')
  const [role, setRole] = useState('user')
  const [confirmDelete, setConfirmDelete] = useState('')

  const closeDlg = () => {
    setDlg(null)
    setPw('')
    setConfirmDelete('')
  }

  const act = async (fn: () => Promise<unknown>) => {
    try {
      await fn()
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    }
    closeDlg()
    setMenuFor(null)
    onChanged()
  }

  if (users.length === 0) return <EmptyState text="没有用户" />
  return (
    <>
      <Stack spacing={1}>
        {users.map((u) => (
          <Stack
            key={u.id}
            direction="row"
            alignItems="center"
            spacing={1.5}
            sx={{ bgcolor: 'aura.surfaceContainer', borderRadius: 3, p: 1.5 }}
          >
            <Avatar sx={{ width: 36, height: 36 }}>{u.username.slice(0, 1).toUpperCase()}</Avatar>
            <Box sx={{ flex: 1, minWidth: 0 }}>
              <Stack direction="row" spacing={1} alignItems="center">
                <Typography variant="body2" fontWeight={600} noWrap>{u.username}</Typography>
                {u.role === 'admin' && <Chip size="small" color="primary" label="管理员" />}
                {u.tier !== 'free' && <Chip size="small" label={u.tier} />}
                {u.status === 'disabled' && <Chip size="small" color="error" label="已禁用" />}
                {u.username === me && <Chip size="small" variant="outlined" label="我" />}
              </Stack>
              <Typography variant="caption" color="text.secondary">
                注册于 {u.created_at?.slice(0, 10) || '—'}
                {u.last_login_at ? ` · 最近登录 ${u.last_login_at.slice(0, 16).replace('T', ' ')}` : ''}
              </Typography>
            </Box>
            {u.username !== me && (
              <Switch
                checked={u.status !== 'disabled'}
                onChange={(e) => void act(() => account.admin.setStatus(u.id, e.target.checked ? 'active' : 'disabled'))}
                size="small"
              />
            )}
            <IconButton
              edge="end"
              aria-label="用户操作"
              onClick={(e) => {
                setMenuFor(u)
                setAnchor(e.currentTarget)
              }}
            >
              <MoreVertIcon />
            </IconButton>
          </Stack>
        ))}
      </Stack>

      <Menu anchorEl={anchor} open={!!menuFor && !!anchor} onClose={() => setMenuFor(null)}>
        <MenuItem
          onClick={() => {
            setRole(menuFor?.role === 'admin' ? 'user' : 'admin')
            setDlg('role')
          }}
        >
          <ListItemIcon><AdminPanelSettingsIcon fontSize="small" /></ListItemIcon>
          设为{menuFor?.role === 'admin' ? '普通用户' : '管理员'}
        </MenuItem>
        <MenuItem onClick={() => setDlg('tier')}>
          <ListItemIcon><StarIcon fontSize="small" /></ListItemIcon>
          设置等级（VIP 预留）
        </MenuItem>
        <MenuItem onClick={() => setDlg('password')}>
          <ListItemIcon><KeyIcon fontSize="small" /></ListItemIcon>
          重置密码
        </MenuItem>
        <MenuItem sx={{ color: 'error.main' }} onClick={() => setDlg('delete')}>
          <ListItemIcon><DeleteOutlineIcon fontSize="small" /></ListItemIcon>
          删除用户
        </MenuItem>
      </Menu>

      <Dialog open={dlg === 'password'} onClose={closeDlg} maxWidth="xs" fullWidth>
        <DialogTitle>重置 {menuFor?.username} 的密码</DialogTitle>
        <DialogContent>
          <TextField autoFocus fullWidth size="small" type="password" value={pw} onChange={(e) => setPw(e.target.value)} sx={{ mt: 1 }} />
        </DialogContent>
        <DialogActions>
          <Button onClick={closeDlg}>取消</Button>
          <Button
            variant="contained"
            disabled={pw.length < 6}
            onClick={() => void act(() => account.admin.resetPassword(menuFor!.id, pw))}
          >
            重置
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={dlg === 'tier'} onClose={closeDlg} maxWidth="xs" fullWidth>
        <DialogTitle>设置 {menuFor?.username} 的等级</DialogTitle>
        <DialogContent>
          <TextField
            autoFocus
            fullWidth
            size="small"
            value={tier}
            onChange={(e) => setTier(e.target.value)}
            helperText="free 为普通等级，其他值视为 VIP（预留）"
            sx={{ mt: 1 }}
          />
        </DialogContent>
        <DialogActions>
          <Button onClick={closeDlg}>取消</Button>
          <Button
            variant="contained"
            onClick={() => void act(() => account.admin.setTier(menuFor!.id, (tier.trim() || 'free') as 'free' | 'vip'))}
          >
            保存
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={dlg === 'role'} onClose={closeDlg} maxWidth="xs" fullWidth>
        <DialogTitle>更改 {menuFor?.username} 的角色</DialogTitle>
        <DialogContent>
          <TextField select fullWidth size="small" value={role} onChange={(e) => setRole(e.target.value)} sx={{ mt: 1 }}>
            <MenuItem value="user">普通用户</MenuItem>
            <MenuItem value="admin">管理员</MenuItem>
          </TextField>
        </DialogContent>
        <DialogActions>
          <Button onClick={closeDlg}>取消</Button>
          <Button variant="contained" onClick={() => void act(() => account.admin.setRole(menuFor!.id, role as 'admin' | 'user'))}>
            保存
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={dlg === 'delete'} onClose={closeDlg} maxWidth="xs" fullWidth>
        <DialogTitle>删除 {menuFor?.username}</DialogTitle>
        <DialogContent>
          <DialogContentText>
            该用户的所有数据（收藏、历史、笔记、下载任务）将被删除，且不可恢复。输入用户名以确认：
          </DialogContentText>
          <TextField autoFocus fullWidth size="small" value={confirmDelete} onChange={(e) => setConfirmDelete(e.target.value)} sx={{ mt: 2 }} />
        </DialogContent>
        <DialogActions>
          <Button onClick={closeDlg}>取消</Button>
          <Button
            color="error"
            variant="contained"
            disabled={confirmDelete !== menuFor?.username}
            onClick={() => void act(() => account.admin.deleteUser(menuFor!.id))}
          >
            永久删除
          </Button>
        </DialogActions>
      </Dialog>
    </>
  )
}
