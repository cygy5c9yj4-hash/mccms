import { lazy, Suspense, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Link as RouterLink, NavLink, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import AppBar from '@mui/material/AppBar'
import Avatar from '@mui/material/Avatar'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import ButtonBase from '@mui/material/ButtonBase'
import Chip from '@mui/material/Chip'
import Divider from '@mui/material/Divider'
import Drawer from '@mui/material/Drawer'
import IconButton from '@mui/material/IconButton'
import List from '@mui/material/List'
import ListItemButton from '@mui/material/ListItemButton'
import ListItemIcon from '@mui/material/ListItemIcon'
import ListItemText from '@mui/material/ListItemText'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Paper from '@mui/material/Paper'
import Stack from '@mui/material/Stack'
import Toolbar from '@mui/material/Toolbar'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import useMediaQuery from '@mui/material/useMediaQuery'
import { alpha, useTheme } from '@mui/material/styles'
import DarkModeIcon from '@mui/icons-material/DarkMode'
import DownloadIcon from '@mui/icons-material/Download'
import ExploreIcon from '@mui/icons-material/Explore'
import AdminPanelSettingsIcon from '@mui/icons-material/AdminPanelSettings'
import FavoriteIcon from '@mui/icons-material/Favorite'
import HistoryIcon from '@mui/icons-material/History'
import HomeIcon from '@mui/icons-material/Home'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import CardMembershipIcon from '@mui/icons-material/CardMembership'
import LightModeIcon from '@mui/icons-material/LightMode'
import LogoutIcon from '@mui/icons-material/Logout'
import LoginIcon from '@mui/icons-material/Login'
import MenuIcon from '@mui/icons-material/Menu'
import MoreHorizIcon from '@mui/icons-material/MoreHoriz'
import SearchIcon from '@mui/icons-material/Search'
import SettingsIcon from '@mui/icons-material/Settings'
import ShuffleIcon from '@mui/icons-material/Shuffle'
import StarIcon from '@mui/icons-material/Star'
import SwipeVerticalIcon from '@mui/icons-material/SwipeVertical'
import WorkspacePremiumIcon from '@mui/icons-material/WorkspacePremium'
import WhatshotIcon from '@mui/icons-material/Whatshot'
import { useAuth } from './auth'
import { useThemeMode } from './mode'
import { useSiteConfig } from './siteConfig'
import { BRAND_GRADIENT, VIP_ACCENT } from './theme'
import { CenterLoading, OnlineStat } from './components'
import Announcement from './components/Announcement'

const Home = lazy(() => import('./pages/Home'))
const Search = lazy(() => import('./pages/Search'))
const Categories = lazy(() => import('./pages/Categories'))
const Leaderboard = lazy(() => import('./pages/Leaderboard'))
const Latest = lazy(() => import('./pages/Latest'))
const RandomPage = lazy(() => import('./pages/RandomPage'))
const Recommend = lazy(() => import('./pages/Recommend'))
const ComicDetail = lazy(() => import('./pages/ComicDetail'))
const Reader = lazy(() => import('./pages/Reader'))
const Favorites = lazy(() => import('./pages/Favorites'))
const AuraHistory = lazy(() => import('./pages/AuraHistory'))
const Downloads = lazy(() => import('./pages/Downloads'))
const Settings = lazy(() => import('./pages/Settings'))
const Login = lazy(() => import('./pages/Login'))
const NovelList = lazy(() => import('./pages/NovelList'))
const NovelDetail = lazy(() => import('./pages/NovelDetail'))
const NovelReader = lazy(() => import('./pages/NovelReader'))
const About = lazy(() => import('./pages/About'))
const Admin = lazy(() => import('./pages/Admin'))
const Vip = lazy(() => import('./pages/Vip'))

// Material Design 3 自适应导航：
// compact(<md) 底部 Navigation Bar / medium(md–lg) Navigation Rail /
// expanded(≥lg) Navigation Drawer；导航区容器 = surface container，
// 激活态 = secondaryContainer 胶囊 indicator。
const DRAWER_WIDTH = 260
const RAIL_WIDTH = 88

interface NavItem {
  to: string
  label: string
  icon: ReactNode
  /** 会员入口用「玫瑰金」描边 + 淡填充突出显示，与其它导航区分开 */
  highlight?: boolean
}

// 导航只保留本后端真正支持的能力（小说 / 阅读笔记 / 收藏夹 / 阅读历史依赖上游
// JM 的账号体系，本后端没有实现，已从导航移除）。
const NAV_BROWSE: NavItem[] = [
  { to: '/', label: '首页', icon: <HomeIcon /> },
  { to: '/search', label: '搜索', icon: <SearchIcon /> },
  { to: '/categories', label: '分类', icon: <ExploreIcon /> },
  { to: '/leaderboard', label: '排行榜', icon: <WhatshotIcon /> },
  { to: '/latest', label: '最新', icon: <SwipeVerticalIcon /> },
  { to: '/random', label: '随机', icon: <ShuffleIcon /> },
]

const NAV_MINE: NavItem[] = [
  { to: '/favorites', label: '收藏', icon: <FavoriteIcon /> },
  { to: '/history', label: '历史', icon: <HistoryIcon /> },
  { to: '/vip', label: '会员', icon: <CardMembershipIcon />, highlight: true },
  { to: '/downloads', label: '下载管理', icon: <DownloadIcon /> },
  { to: '/settings', label: '设置', icon: <SettingsIcon /> },
  { to: '/about', label: '关于', icon: <InfoOutlinedIcon /> },
]

// 底部栏 / 导航栏最多 5 个目的地（M3 规范），其余入口收进「更多」抽屉
const NAV_PRIMARY: NavItem[] = [NAV_BROWSE[0], NAV_BROWSE[1], NAV_BROWSE[2], NAV_MINE[0]]

function isActive(to: string, pathname: string): boolean {
  return to === '/' ? pathname === '/' : pathname.startsWith(to)
}

function Logo() {
  const { name } = useSiteConfig()

  return (
    <Typography
      variant="h6"
      component={RouterLink}
      to="/"
      sx={{
        display: 'inline-flex',
        alignItems: 'baseline',
        gap: 0.5,
        textDecoration: 'none',
        fontWeight: 700,
        letterSpacing: '-0.02em',
        '& span': {
          background: BRAND_GRADIENT,
          WebkitBackgroundClip: 'text',
          backgroundClip: 'text',
          color: 'transparent',
        },
      }}
    >
      <span>{name}</span>
    </Typography>
  )
}

function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <Typography
      sx={{
        px: 3,
        pt: 2,
        pb: 0.5,
        fontSize: 12.5,
        fontWeight: 600,
        letterSpacing: '0.05em',
        color: 'text.secondary',
      }}
    >
      {children}
    </Typography>
  )
}

/** Navigation Bar / Rail 共用的胶囊目的地按钮 */
function NavPill({
  item,
  active,
  onClick,
}: {
  item: NavItem
  active: boolean
  onClick: () => void
}) {
  return (
    <ButtonBase
      onClick={onClick}
      aria-label={item.label}
      aria-current={active ? 'page' : undefined}
      sx={{
        flexDirection: 'column',
        gap: 0.5,
        py: 0.75,
        px: 0.5,
        minWidth: 60,
        borderRadius: 2.5,
        WebkitTapHighlightColor: 'transparent',
      }}
    >
      <Box
        sx={{
          width: 58,
          height: 32,
          borderRadius: '999px',
          display: 'grid',
          placeItems: 'center',
          bgcolor: active ? 'aura.secondaryContainer' : 'transparent',
          transition: 'background-color .2s ease',
          '& .MuiSvgIcon-root': {
            fontSize: 22,
            color: active ? 'aura.onSecondaryContainer' : 'aura.onSurfaceVariant',
          },
        }}
      >
        {item.icon}
      </Box>
      <Typography
        sx={{
          fontSize: 12,
          lineHeight: 1,
          fontWeight: active ? 600 : 500,
          color: active ? 'text.primary' : 'text.secondary',
        }}
      >
        {item.label}
      </Typography>
    </ButtonBase>
  )
}

function NavListItems({ items, onNavigate }: { items: NavItem[]; onNavigate?: () => void }) {
  const location = useLocation()
  return (
    <List disablePadding>
      {items.map((item) => (
        <ListItemButton
          key={item.to}
          component={NavLink}
          to={item.to}
          end={item.to === '/'}
          onClick={onNavigate}
          selected={isActive(item.to, location.pathname)}
          sx={
            item.highlight
              ? {
                  mx: 1.5,
                  my: 0.75,
                  borderRadius: 999,
                  color: 'primary.main',
                  background: (t) =>
                    alpha(t.palette.primary.main, t.palette.mode === 'dark' ? 0.12 : 0.07),
                  border: '1px solid',
                  borderColor: (t) =>
                    alpha(VIP_ACCENT, t.palette.mode === 'dark' ? 0.36 : 0.44),
                  '& .MuiListItemIcon-root': { color: VIP_ACCENT },
                  '&:hover': {
                    background: (t) =>
                      alpha(t.palette.primary.main, t.palette.mode === 'dark' ? 0.18 : 0.12),
                  },
                  '&.Mui-selected': {
                    background: (t) =>
                      alpha(t.palette.primary.main, t.palette.mode === 'dark' ? 0.22 : 0.14),
                    color: 'primary.main',
                  },
                  '&.Mui-selected:hover': {
                    background: (t) =>
                      alpha(t.palette.primary.main, t.palette.mode === 'dark' ? 0.26 : 0.18),
                  },
                }
              : undefined
          }
        >
          <ListItemIcon>{item.icon}</ListItemIcon>
          <ListItemText
            primary={item.label}
            primaryTypographyProps={{ fontSize: 14.5, fontWeight: item.highlight ? 700 : 400 }}
          />
          {item.highlight && (
            <Box
              component="span"
              sx={{
                ml: 1,
                px: 0.85,
                py: 0.1,
                borderRadius: 999,
                fontSize: 10,
                fontWeight: 800,
                letterSpacing: '0.08em',
                lineHeight: 1.6,
                color: VIP_ACCENT,
                border: '1px solid',
                borderColor: alpha(VIP_ACCENT, 0.5),
              }}
            >
              VIP
            </Box>
          )}
        </ListItemButton>
      ))}
    </List>
  )
}

/** expanded 抽屉内容：浏览 / 我的 两个分区 */
function DrawerContent({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <Box sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <Toolbar sx={{ px: 2.5 }}>
        <Logo />
      </Toolbar>
      <Box sx={{ flexGrow: 1, overflowY: 'auto', pb: 2 }}>
        <SectionLabel>浏览</SectionLabel>
        <NavListItems items={NAV_BROWSE} onNavigate={onNavigate} />
        <Divider sx={{ mx: 3, mt: 1.5, opacity: 0.7 }} />
        <SectionLabel>我的</SectionLabel>
        <NavListItems items={NAV_MINE} onNavigate={onNavigate} />
      </Box>
    </Box>
  )
}

/** medium 断点：Navigation Rail */
function RailContent({ onMore }: { onMore: () => void }) {
  const location = useLocation()
  const navigate = useNavigate()
  return (
    <Box
      sx={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        py: 1.5,
        gap: 0.5,
      }}
    >
      <IconButton
        onClick={onMore}
        aria-label="打开全部导航"
        sx={{ mb: 1, color: 'aura.onSurfaceVariant' }}
      >
        <MenuIcon />
      </IconButton>
      {NAV_PRIMARY.map((item) => (
        <NavPill
          key={item.to}
          item={item}
          active={isActive(item.to, location.pathname)}
          onClick={() => navigate(item.to)}
        />
      ))}
      <Box sx={{ flexGrow: 1 }} />
      <NavPill item={{ to: '#', label: '更多', icon: <MoreHorizIcon /> }} active={false} onClick={onMore} />
    </Box>
  )
}

/** compact 断点：底部 Navigation Bar */
function BottomBar({ onMore }: { onMore: () => void }) {
  const location = useLocation()
  const navigate = useNavigate()
  return (
    <Paper
      square
      elevation={0}
      sx={{
        position: 'fixed',
        left: 0,
        right: 0,
        bottom: 0,
        zIndex: (t) => t.zIndex.appBar,
        bgcolor: 'aura.surfaceContainer',
        borderTop: '1px solid',
        borderColor: 'divider',
        pb: 'env(safe-area-inset-bottom)',
        display: { xs: 'block', md: 'none' },
      }}
    >
      <Box sx={{ display: 'flex', justifyContent: 'space-around', alignItems: 'flex-start', pt: 0.5 }}>
        {NAV_PRIMARY.map((item) => (
          <NavPill
            key={item.to}
            item={item}
            active={isActive(item.to, location.pathname)}
            onClick={() => navigate(item.to)}
          />
        ))}
        <NavPill item={{ to: '#', label: '更多', icon: <MoreHorizIcon /> }} active={false} onClick={onMore} />
      </Box>
    </Paper>
  )
}

function UserArea() {
  const { user, loading, logout } = useAuth()
  const navigate = useNavigate()
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)

  if (loading) {
    return <Chip size="small" label="…" sx={{ opacity: 0.5 }} />
  }
  if (!user) {
    return (
      <Button component={RouterLink} to="/login" size="small" variant="contained" startIcon={<LoginIcon />}>
        登录
      </Button>
    )
  }
  return (
    <>
      <Tooltip title={user.username}>
        <Chip
          clickable
          avatar={
            <Avatar sx={{ bgcolor: 'primary.main', color: 'primary.contrastText', fontSize: 13 }}>
              {user.username.slice(0, 1).toUpperCase()}
            </Avatar>
          }
          label={user.username}
          onClick={(e) => setAnchorEl(e.currentTarget)}
          sx={{ maxWidth: 160 }}
        />
      </Tooltip>
      <Menu anchorEl={anchorEl} open={!!anchorEl} onClose={() => setAnchorEl(null)}>
        {user.is_admin && (
          <MenuItem
            onClick={() => {
              setAnchorEl(null)
              navigate('/admin')
            }}
          >
            <ListItemIcon>
              <AdminPanelSettingsIcon fontSize="small" />
            </ListItemIcon>
            管理后台
          </MenuItem>
        )}
        <MenuItem
          onClick={() => {
            setAnchorEl(null)
            void logout()
          }}
        >
          <ListItemIcon>
            <LogoutIcon fontSize="small" />
          </ListItemIcon>
          退出登录
        </MenuItem>
      </Menu>
    </>
  )
}

export default function App() {
  const { name } = useSiteConfig()
  const theme = useTheme()
  const isRail = useMediaQuery(theme.breakpoints.up('md'))
  const isExpanded = useMediaQuery(theme.breakpoints.up('lg'))
  const [drawerOpen, setDrawerOpen] = useState(false)
  const { mode, toggle } = useThemeMode()
  const location = useLocation()
  const isReader = location.pathname.startsWith('/reader/') || location.pathname.startsWith('/novel_reader/')

  const drawerPaper = useMemo(
    () => ({ width: DRAWER_WIDTH, bgcolor: 'aura.surfaceContainerLow' }),
    [],
  )

  return (
    <Box sx={{ display: 'flex', minHeight: '100dvh' }}>
      <Announcement />
      {!isReader && (
        <AppBar position="fixed" sx={{ zIndex: (t) => t.zIndex.drawer + 1 }}>
          <Toolbar sx={{ gap: 1 }}>
            <Logo />
            <Box sx={{ flexGrow: 1 }} />
            <Tooltip title="开通会员，畅读全部内容">
              <Button
                size="small"
                component={NavLink}
                to="/vip"
                aria-label="成为会员"
                startIcon={<WorkspacePremiumIcon sx={{ fontSize: 18, color: VIP_ACCENT }} />}
                sx={{
                  flexShrink: 0,
                  minWidth: 0,
                  px: { xs: 1, sm: 1.75 },
                  borderRadius: 999,
                  fontWeight: 700,
                  fontSize: 13,
                  whiteSpace: 'nowrap',
                  color: 'primary.main',
                  background: (t) =>
                    alpha(t.palette.primary.main, t.palette.mode === 'dark' ? 0.12 : 0.07),
                  border: '1px solid',
                  borderColor: (t) =>
                    alpha(VIP_ACCENT, t.palette.mode === 'dark' ? 0.34 : 0.42),
                  '&:hover': {
                    background: (t) =>
                      alpha(t.palette.primary.main, t.palette.mode === 'dark' ? 0.18 : 0.12),
                  },
                }}
              >
                <Box component="span" sx={{ display: { xs: 'none', sm: 'inline' } }}>
                  成为会员
                </Box>
              </Button>
            </Tooltip>
            <Tooltip title={mode === 'dark' ? '切换到浅色' : '切换到深色'}>
              <IconButton onClick={toggle} aria-label="切换主题">
                {mode === 'dark' ? <LightModeIcon /> : <DarkModeIcon />}
              </IconButton>
            </Tooltip>
          <UserArea />
          </Toolbar>
        </AppBar>
      )}

      {isExpanded && !isReader && (
        <Drawer
          variant="permanent"
          sx={{
            width: DRAWER_WIDTH,
            flexShrink: 0,
            '& .MuiDrawer-paper': { ...drawerPaper, position: 'relative', mt: '64px' },
          }}
        >
          <DrawerContent />
        </Drawer>
      )}

      {!isExpanded && isRail && !isReader && (
        <Drawer
          variant="permanent"
          sx={{
            width: RAIL_WIDTH,
            flexShrink: 0,
            '& .MuiDrawer-paper': {
              width: RAIL_WIDTH,
              position: 'relative',
              mt: '64px',
              bgcolor: 'aura.surfaceContainer',
              boxSizing: 'border-box',
            },
          }}
        >
          <RailContent onMore={() => setDrawerOpen(true)} />
        </Drawer>
      )}

      {!isRail && !isReader && <BottomBar onMore={() => setDrawerOpen(true)} />}

      {!isExpanded && !isReader && (
        <Drawer open={drawerOpen} onClose={() => setDrawerOpen(false)} slotProps={{ paper: { sx: drawerPaper } }}>
          <DrawerContent onNavigate={() => setDrawerOpen(false)} />
        </Drawer>
      )}

      <Box
        component="main"
        sx={{
          flexGrow: 1,
          minWidth: 0,
          p: { xs: 2, sm: 3 },
          pb: { xs: 'calc(92px + env(safe-area-inset-bottom))', md: 3 },
          maxWidth: 1400,
          mx: 'auto',
          ...(isReader && { p: 0, maxWidth: 'none' }),
        }}
      >
        {!isReader && <Toolbar />}
        <Suspense fallback={<CenterLoading />}>
          <Routes>
            <Route path="/" element={<Home />} />
            <Route path="/search" element={<Search />} />
            <Route path="/categories" element={<Categories />} />
            <Route path="/leaderboard" element={<Leaderboard />} />
            <Route path="/latest" element={<Latest />} />
            <Route path="/random" element={<RandomPage />} />
            <Route path="/recommend" element={<Recommend />} />
            <Route path="/comic/:comicId" element={<ComicDetail />} />
            <Route path="/reader/:chapterId" element={<Reader />} />
            <Route path="/novels" element={<NovelList />} />
            <Route path="/novel/:novelId" element={<NovelDetail />} />
            <Route path="/novel_reader/:chapterId" element={<NovelReader />} />
            <Route path="/favorites" element={<Favorites />} />
            <Route path="/history" element={<AuraHistory />} />
            <Route path="/vip" element={<Vip />} />
            <Route path="/downloads" element={<Downloads />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/about" element={<About />} />
            <Route path="/admin" element={<Admin />} />
            <Route path="/login" element={<Login />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Suspense>
        {!isReader && (
          <Box component="footer" sx={{ py: 5, textAlign: 'center' }}>
            <Stack direction="row" spacing={1} justifyContent="center" alignItems="center">
              <StarIcon fontSize="small" sx={{ opacity: 0.4 }} />
              <Typography variant="caption" color="text.secondary">
                由 Material Design 3 与 Go 驱动
              </Typography>
            </Stack>
            <Typography
              component={RouterLink}
              to="/about"
              variant="caption"
              sx={{
                display: 'inline-block',
                mt: 1,
                color: 'text.secondary',
                textDecoration: 'none',
                '&:hover': { color: 'primary.main' },
              }}
            >
              关于 {name}
            </Typography>
            <Box sx={{ mt: 1.5 }}>
              <OnlineStat />
            </Box>
          </Box>
        )}
      </Box>
    </Box>
  )
}
