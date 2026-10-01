// 会员中心：一屏之内看完「当前状态 → 兑换卡密 → 成为/续费会员」。
import { useCallback, useEffect, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Card from '@mui/material/Card'
import CardContent from '@mui/material/CardContent'
import Chip from '@mui/material/Chip'
import CircularProgress from '@mui/material/CircularProgress'
import Dialog from '@mui/material/Dialog'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import IconButton from '@mui/material/IconButton'
import Snackbar from '@mui/material/Snackbar'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import CardMembershipIcon from '@mui/icons-material/CardMembership'
import CheckCircleIcon from '@mui/icons-material/CheckCircle'
import OpenInNewIcon from '@mui/icons-material/OpenInNew'
import RedeemIcon from '@mui/icons-material/Redeem'
import WorkspacePremiumIcon from '@mui/icons-material/WorkspacePremium'
import { SectionTitle } from '../components'
import { alpha } from '@mui/material/styles'
import { AFDIAN_URL, VIP_ACCENT, VIP_GRADIENT, VIP_SHEEN_DARK, VIP_SHEEN_LIGHT } from '../theme'
import { useAuth } from '../auth'
import { vip, type AfdianBinding, type VipStatus } from '../vip'

export default function Vip() {
  const { user } = useAuth()
  const [status, setStatus] = useState<VipStatus | null>(null)
  const [binding, setBinding] = useState<AfdianBinding | null>(null)
  const [loading, setLoading] = useState(true)
  const [code, setCode] = useState('')
  const [redeeming, setRedeeming] = useState(false)
  const [msg, setMsg] = useState<{ kind: 'success' | 'error'; text: string } | null>(null)
  const [joinOpen, setJoinOpen] = useState(false)

  const reload = useCallback(async () => {
    if (!user) {
      setStatus(null)
      setLoading(false)
      return
    }
    setLoading(true)
    try {
      const [s, b] = await Promise.all([vip.status(), vip.afdian().catch(() => null)])
      setStatus(s)
      setBinding(b)
    } catch {
      setStatus(null)
    } finally {
      setLoading(false)
    }
  }, [user])

  useEffect(() => {
    void reload()
  }, [reload])

  const active = !!status?.active
  const daysLeft = status?.days_left ?? 0

  const doRedeem = async () => {
    const value = code.trim()
    if (!value) return
    setRedeeming(true)
    try {
      const r = await vip.redeem(value)
      setMsg({ kind: 'success', text: `兑换成功，会员延长至 ${fmtDate(r.expires_at)}` })
      setCode('')
      await reload()
    } catch (e) {
      setMsg({ kind: 'error', text: errText(e) })
    } finally {
      setRedeeming(false)
    }
  }

  if (!user) {
    return (
      <Box sx={{ maxWidth: 620, mx: 'auto' }}>
        <SectionTitle>
          <CardMembershipIcon sx={{ color: 'primary.main' }} /> 会员中心
        </SectionTitle>
        <Card sx={{ borderRadius: 5 }}>
          <CardContent sx={{ p: 4 }}>
            <Typography color="text.secondary">登录后即可查看会员状态、兑换卡密。</Typography>
          </CardContent>
        </Card>
      </Box>
    )
  }

  return (
    <Box sx={{ maxWidth: 620, mx: 'auto' }}>
      <SectionTitle>
        <CardMembershipIcon sx={{ color: 'primary.main' }} /> 会员中心
      </SectionTitle>

      {/* 当前状态 */}
      <Card
        sx={{
          borderRadius: 5,
          mb: 2.5,
          backgroundImage: (t) =>
            active ? (t.palette.mode === 'dark' ? VIP_SHEEN_DARK : VIP_SHEEN_LIGHT) : 'none',
          border: '1px solid',
          borderColor: (t) =>
            active ? alpha(VIP_ACCENT, t.palette.mode === 'dark' ? 0.42 : 0.5) : t.palette.divider,
          boxShadow: 'none',
        }}
      >
        <CardContent sx={{ p: 3.5 }}>
          {loading ? (
            <CircularProgress size={22} sx={{ color: active ? VIP_ACCENT : 'primary.main' }} />
          ) : (
            <Stack direction="row" alignItems="center" spacing={2}>
              <Box
                sx={{
                  width: 56,
                  height: 56,
                  flex: '0 0 auto',
                  borderRadius: '50%',
                  display: 'grid',
                  placeItems: 'center',
                  bgcolor: (t) =>
                    active
                      ? alpha(VIP_ACCENT, t.palette.mode === 'dark' ? 0.16 : 0.14)
                      : t.palette.action.hover,
                  border: '1px solid',
                  borderColor: (t) => (active ? alpha(VIP_ACCENT, 0.42) : t.palette.divider),
                }}
              >
                {active ? (
                  <WorkspacePremiumIcon sx={{ fontSize: 30, color: VIP_ACCENT }} />
                ) : (
                  <CardMembershipIcon sx={{ fontSize: 30, color: 'text.disabled' }} />
                )}
              </Box>
              <Box sx={{ flex: 1, minWidth: 0 }}>
                <Stack direction="row" alignItems="center" spacing={1}>
                  <Typography variant="h6" sx={{ fontWeight: 800, letterSpacing: '-0.01em' }}>
                    {active ? '会员有效' : '当前为免费用户'}
                  </Typography>
                  {active && (
                    <Chip
                      size="small"
                      label="VIP"
                      variant="outlined"
                      sx={{
                        height: 20,
                        fontSize: 10,
                        fontWeight: 800,
                        letterSpacing: '0.08em',
                        color: VIP_ACCENT,
                        bgcolor: alpha(VIP_ACCENT, 0.1),
                        borderColor: (t) =>
                          alpha(VIP_ACCENT, t.palette.mode === 'dark' ? 0.5 : 0.55),
                      }}
                    />
                  )}
                </Stack>
                <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
                  {active ? (
                    <>
                      剩余{' '}
                      <Box component="span" sx={{ color: 'primary.main', fontWeight: 700 }}>
                        {daysLeft} 天
                      </Box>
                      ，到期 {fmtDate(status?.expires_at)}，续费自动顺延
                    </>
                  ) : (
                    '开通会员即可畅读全部内容'
                  )}
                </Typography>
              </Box>
            </Stack>
          )}
        </CardContent>
      </Card>

      {/* 成为 / 续费 */}
      <Button
        fullWidth
        size="large"
        variant="contained"
        onClick={() => setJoinOpen(true)}
        startIcon={<WorkspacePremiumIcon />}
        sx={{
          borderRadius: 999,
          py: 1.4,
          fontWeight: 700,
          color: '#fff',
          background: VIP_GRADIENT,
          boxShadow: '0 10px 24px -16px rgba(210,58,110,.75)',
          '&:hover': { background: VIP_GRADIENT, filter: 'brightness(1.05)' },
        }}
      >
        {active ? '续费会员' : '成为会员'}
      </Button>

      {/* 卡密兑换 */}
      <Card sx={{ borderRadius: 5, mt: 2.5 }}>
        <CardContent sx={{ p: 3 }}>
          <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 1.5 }}>
            <RedeemIcon color="primary" fontSize="small" />
            <Typography sx={{ fontWeight: 700 }}>卡密兑换</Typography>
          </Stack>
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.5}>
            <TextField
              fullWidth
              size="small"
              placeholder="MCCMS-XXXX-XXXX-XXXX"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void doRedeem()
              }}
            />
            <Button
              variant="outlined"
              onClick={() => void doRedeem()}
              disabled={redeeming || !code.trim()}
              sx={{ borderRadius: 999, px: 3, whiteSpace: 'nowrap' }}
            >
              {redeeming ? '兑换中…' : '兑换'}
            </Button>
          </Stack>
          <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1.5 }}>
            已有卡密直接兑换；兑换成功后会员天数自动顺延。
          </Typography>
        </CardContent>
      </Card>

      {/* 爱发电绑定状态（轻量） */}
      {binding?.bound && (
        <Stack direction="row" spacing={1} alignItems="center" sx={{ mt: 2, justifyContent: 'center' }}>
          <Chip
            size="small"
            color="success"
            variant="outlined"
            label={`已绑定爱发电 ${binding.masked_id ?? ''}`}
            onDelete={() => {
              void vip.unbindAfdian().then(() => {
                setMsg({ kind: 'success', text: '已解绑，已发放的会员不受影响' })
                void reload()
              })
            }}
          />
        </Stack>
      )}

      <JoinDialog
        open={joinOpen}
        onClose={() => setJoinOpen(false)}
        username={user.username}
        renew={active}
      />

      <Snackbar
        open={!!msg}
        autoHideDuration={4000}
        onClose={() => setMsg(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      >
        <Alert severity={msg?.kind ?? 'success'} onClose={() => setMsg(null)} variant="filled">
          {msg?.text}
        </Alert>
      </Snackbar>
    </Box>
  )
}

// 成为会员：复制用户名 → 粘贴到爱发电「自定义信息」→ 复制并跳转。
function JoinDialog({
  open,
  onClose,
  username,
  renew,
}: {
  open: boolean
  onClose: () => void
  username: string
  renew: boolean
}) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(username)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      setCopied(false)
    }
  }

  const copyAndGo = async () => {
    try {
      await navigator.clipboard.writeText(username)
    } catch {
      /* 剪贴板不可用时仍允许跳转 */
    }
    window.open(AFDIAN_URL, '_blank', 'noopener,noreferrer')
    onClose()
  }

  return (
    <Dialog open={open} onClose={onClose} maxWidth="xs" fullWidth PaperProps={{ sx: { borderRadius: 4 } }}>
      <DialogTitle sx={{ fontWeight: 800 }}>{renew ? '续费会员' : '成为会员'}</DialogTitle>
      <DialogContent>
        <Typography color="text.secondary" sx={{ mb: 2, lineHeight: 1.8 }}>
          三步即可开通，无需注册爱发电账号以外的任何操作：
        </Typography>

        <Step n={1} title="复制你的用户名">
          <Stack direction="row" spacing={1} alignItems="center" sx={{ mt: 1 }}>
            <TextField
              value={username}
              size="small"
              fullWidth
              InputProps={{ readOnly: true }}
              onFocus={(e) => e.target.select()}
            />
            <IconButton onClick={() => void copy()} color={copied ? 'success' : 'default'}>
              {copied ? <CheckCircleIcon /> : <ContentCopyIcon />}
            </IconButton>
          </Stack>
        </Step>

        <Step n={2} title="粘贴到爱发电的「自定义信息」">
          <Typography variant="body2" color="text.secondary" sx={{ lineHeight: 1.8 }}>
            在赞助页面找到「自定义信息」一栏，把用户名粘贴进去。这样系统才能把赞助发放到你的账号。
          </Typography>
        </Step>

        <Step n={3} title="复制并跳转">
          <Button
            fullWidth
            variant="contained"
            onClick={() => void copyAndGo()}
            endIcon={<OpenInNewIcon />}
            sx={{ mt: 1, borderRadius: 999, color: '#fff', background: VIP_GRADIENT }}
          >
            复制并跳转
          </Button>
        </Step>

        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 2 }}>
          复制按钮已同时复制用户名，跳转后直接粘贴即可。
        </Typography>
      </DialogContent>
    </Dialog>
  )
}

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <Box sx={{ mb: 2 }}>
      <Stack direction="row" spacing={1.2} alignItems="center">
        <Box
          sx={{
            width: 24,
            height: 24,
            borderRadius: '50%',
            bgcolor: 'primary.main',
            color: 'primary.contrastText',
            fontSize: 13,
            fontWeight: 700,
            display: 'grid',
            placeItems: 'center',
            flex: '0 0 auto',
          }}
        >
          {n}
        </Box>
        <Typography sx={{ fontWeight: 700 }}>{title}</Typography>
      </Stack>
      <Box sx={{ pl: 4.2 }}>{children}</Box>
    </Box>
  )
}

function fmtDate(v?: string | null) {
  if (!v) return '—'
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' })
}

function errText(e: unknown) {
  const any = e as { message?: string; response?: { data?: { msg?: string } } }
  return any?.response?.data?.msg || any?.message || '操作失败，请稍后重试'
}