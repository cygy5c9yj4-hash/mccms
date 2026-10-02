// 会员中心：一屏之内看完「当前状态 → 成为/续费会员 → 卡密兑换」。
// 开通流程极简：复制用户名 → 跳转爱发电 → 在「留言」框粘贴用户名 → 付款。
import { useCallback, useEffect, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Card from '@mui/material/Card'
import CardContent from '@mui/material/CardContent'
import Chip from '@mui/material/Chip'
import CircularProgress from '@mui/material/CircularProgress'
import Collapse from '@mui/material/Collapse'
import Dialog from '@mui/material/Dialog'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Snackbar from '@mui/material/Snackbar'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import CardMembershipIcon from '@mui/icons-material/CardMembership'
import CheckCircleIcon from '@mui/icons-material/CheckCircle'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import OpenInNewIcon from '@mui/icons-material/OpenInNew'
import RedeemIcon from '@mui/icons-material/Redeem'
import WorkspacePremiumIcon from '@mui/icons-material/WorkspacePremium'
import { SectionTitle } from '../components'
import { alpha } from '@mui/material/styles'
import { AFDIAN_URL, VIP_ACCENT, VIP_GRADIENT, VIP_SHEEN_DARK, VIP_SHEEN_LIGHT } from '../theme'
import { useAuth } from '../auth'
import { vip, type AfdianBinding, type VipStatus } from '../vip'
import afdianGuide from '../assets/afdian-guide.png?inline'

export default function Vip() {
  const { user } = useAuth()
  const [status, setStatus] = useState<VipStatus | null>(null)
  const [binding, setBinding] = useState<AfdianBinding | null>(null)
  const [loading, setLoading] = useState(true)
  const [code, setCode] = useState('')
  const [redeeming, setRedeeming] = useState(false)
  const [redeemOpen, setRedeemOpen] = useState(false)
  const [joinOpen, setJoinOpen] = useState(false)
  const [msg, setMsg] = useState<{ kind: 'success' | 'error'; text: string } | null>(null)

  const reload = useCallback(async () => {
    try {
      const s = await vip.status()
      setStatus(s)
      if (user) {
        try {
          setBinding(await vip.afdian())
        } catch {
          setBinding(null)
        }
      }
    } catch (e) {
      setMsg({ kind: 'error', text: errText(e) })
    } finally {
      setLoading(false)
    }
  }, [user])

  useEffect(() => {
    void reload()
  }, [reload])

  const doRedeem = useCallback(async () => {
    const c = code.trim()
    if (!c) return
    setRedeeming(true)
    try {
      const r = await vip.redeem(c)
      setCode('')
      setMsg({ kind: 'success', text: `兑换成功，会员延长 ${r.days} 天` })
      await reload()
    } catch (e) {
      setMsg({ kind: 'error', text: errText(e) })
    } finally {
      setRedeeming(false)
    }
  }, [code, reload])

  if (!user) {
    return (
      <Box>
        <SectionTitle>会员中心</SectionTitle>
        <Card sx={{ borderRadius: 5 }}>
          <CardContent sx={{ p: 4, textAlign: 'center' }}>
            <Typography color="text.secondary" sx={{ mb: 2 }}>
              登录后即可开通会员、兑换卡密。
            </Typography>
            <Button href="/login" variant="contained" sx={{ borderRadius: 999, color: '#fff', background: VIP_GRADIENT }}>
              去登录
            </Button>
          </CardContent>
        </Card>
      </Box>
    )
  }

  if (loading) {
    return (
      <Box sx={{ display: 'grid', placeItems: 'center', py: 8 }}>
        <CircularProgress />
      </Box>
    )
  }

  const active = !!status?.active
  const daysLeft = status?.days_left ?? 0
  const price = fmtPrice(status?.month_price)

  return (
    <Box>
      <SectionTitle>会员中心</SectionTitle>

      {/* 状态卡 */}
      <Card
        sx={{
          position: 'relative',
          overflow: 'hidden',
          borderRadius: 5,
          mb: 2.5,
          border: '1px solid',
          borderColor: (t) => (active ? alpha(VIP_ACCENT, 0.5) : t.palette.divider),
          background: (t) => (active ? (t.palette.mode === 'dark' ? VIP_SHEEN_DARK : VIP_SHEEN_LIGHT) : undefined),
        }}
      >
        <CardContent sx={{ p: 3 }}>
          {status && (
            <Stack direction="row" spacing={2} alignItems="center">
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
        {price ? ` · ¥${price}/月` : ''}
      </Button>

      <Button
        size="small"
        onClick={() => void reload()}
        sx={{ mt: 1.5, mx: 'auto', display: 'block', color: 'text.secondary' }}
      >
        已赞助？点此刷新状态
      </Button>

      {/* 卡密兑换（默认收起） */}
      <Card sx={{ borderRadius: 5, mt: 2.5 }}>
        <CardContent sx={{ p: 3 }}>
          <Stack
            direction="row"
            spacing={1}
            alignItems="center"
            justifyContent="space-between"
            onClick={() => setRedeemOpen((v) => !v)}
            sx={{ cursor: 'pointer' }}
          >
            <Stack direction="row" spacing={1} alignItems="center">
              <RedeemIcon color="primary" fontSize="small" />
              <Typography sx={{ fontWeight: 700 }}>有卡密？点此兑换</Typography>
            </Stack>
            <ExpandMoreIcon
              sx={{
                color: 'text.secondary',
                transition: 'transform .2s',
                transform: redeemOpen ? 'rotate(180deg)' : 'none',
              }}
            />
          </Stack>
          <Collapse in={redeemOpen}>
            <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.5} sx={{ mt: 2 }}>
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
          </Collapse>
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
        price={price}
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

// 开通会员：一键「复制用户名并跳转」，用户在爱发电「留言」框粘贴用户名后付款。
function JoinDialog({
  open,
  onClose,
  username,
  renew,
  price,
}: {
  open: boolean
  onClose: () => void
  username: string
  renew: boolean
  price: string
}) {
  const [copied, setCopied] = useState(false)
  const [left, setLeft] = useState(READ_SECONDS)

  // 阅读倒计时：弹窗打开后需等待 READ_SECONDS 秒才允许点击。
  useEffect(() => {
    if (!open) return
    setLeft(READ_SECONDS)
    setCopied(false)
    const timer = setInterval(() => {
      setLeft((v) => {
        if (v <= 1) {
          clearInterval(timer)
          return 0
        }
        return v - 1
      })
    }, 1000)
    return () => clearInterval(timer)
  }, [open])

  const copyAndGo = async () => {
    if (left > 0) return
    try {
      await navigator.clipboard.writeText(username)
      setCopied(true)
      setTimeout(() => setCopied(false), 2500)
    } catch {
      /* 剪贴板不可用时仍允许跳转，用户可手动复制 */
    }
    window.open(AFDIAN_URL, '_blank', 'noopener,noreferrer')
  }

  return (
    <Dialog open={open} onClose={onClose} maxWidth="xs" fullWidth PaperProps={{ sx: { borderRadius: 4 } }}>
      <DialogTitle sx={{ fontWeight: 800, pb: 0.5 }}>{renew ? '续费会员' : '成为会员'}</DialogTitle>
      <DialogContent>
        {price && (
          <Stack direction="row" alignItems="baseline" spacing={0.5} sx={{ mb: 2 }}>
            <Typography variant="h4" sx={{ fontWeight: 800, color: 'primary.main', letterSpacing: '-0.02em' }}>
              ¥{price}
            </Typography>
            <Typography color="text.secondary">/ 月</Typography>
          </Stack>
        )}

        <Typography sx={{ mt: 2.5, fontWeight: 700 }}>
          在付款页的「留言」框里粘贴用户名，然后付款
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5, lineHeight: 1.7 }}>
          只填用户名，不要加别的内容（该框在爱发电页面上也叫「自定义信息」）。
        </Typography>

        <Box
          component="img"
          src={afdianGuide}
          alt="在留言框粘贴你的用户名"
          loading="lazy"
          sx={{
            display: 'block',
            width: '100%',
            mt: 2,
            borderRadius: 2,
            border: '1px solid',
            borderColor: 'divider',
          }}
        />

        <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 2, lineHeight: 1.7 }}>
          付款后一般几秒内自动开通。若未到账，回到本页点「已赞助？点此刷新状态」。
        </Typography>

        <Button
          fullWidth
          size="large"
          variant="contained"
          disabled={left > 0}
          onClick={() => void copyAndGo()}
          endIcon={left > 0 ? undefined : copied ? <CheckCircleIcon /> : <OpenInNewIcon />}
          sx={{
            mt: 3,
            borderRadius: 999,
            py: 1.4,
            fontWeight: 700,
            color: '#fff',
            background: VIP_GRADIENT,
            '&:hover': { background: VIP_GRADIENT, filter: 'brightness(1.05)' },
            '&.Mui-disabled': { background: VIP_GRADIENT, color: '#fff', opacity: 0.45 },
          }}
        >
          {left > 0
            ? `请先阅读说明（${left}s）`
            : copied
              ? '已复制，去爱发电粘贴付款'
              : '复制用户名并跳转爱发电'}
        </Button>
      </DialogContent>
    </Dialog>
  )
}

const READ_SECONDS = 10

function fmtPrice(v?: string) {
  const s = (v ?? '').trim()
  if (!s) return ''
  const n = Number(s)
  if (!Number.isFinite(n)) return s
  return n.toFixed(2)
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
