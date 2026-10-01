// 管理后台 · VIP：卡密、爱发电订单、手动发放与免费白名单。
import { useEffect, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Divider from '@mui/material/Divider'
import IconButton from '@mui/material/IconButton'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Tooltip from '@mui/material/Tooltip'
import Typography from '@mui/material/Typography'
import BlockIcon from '@mui/icons-material/Block'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline'
import { CenterLoading, EmptyState, SectionTitle } from '../components'
import { adminVip, isCodeUsed, type AfdianOrder, type RedeemCode } from '../vip'

export default function AdminVip() {
  const [codes, setCodes] = useState<RedeemCode[]>([])
  const [orders, setOrders] = useState<AfdianOrder[]>([])
  const [freeComics, setFreeComics] = useState('')
  const [monthPrice, setMonthPrice] = useState('')
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const [count, setCount] = useState(5)
  const [days, setDays] = useState(31)
  const [note, setNote] = useState('')
  const [grantName, setGrantName] = useState('')
  const [grantDays, setGrantDays] = useState(31)
  const [bindFor, setBindFor] = useState<AfdianOrder | null>(null)
  const [bindName, setBindName] = useState('')
  const [syncing, setSyncing] = useState(false)
  const [genCount, setGenCount] = useState(12)
  const [generating, setGenerating] = useState(false)

  // 一键生成免费白名单：跨源挑选，总量固定为 genCount。
  const generateFree = async () => {
    setGenerating(true)
    setErr('')
    setMsg('')
    try {
      const r = await adminVip.generateFreeComics(genCount, [])
      setFreeComics((r.free_comics ?? []).join(', '))
      setMsg(`已生成 ${r.count} 部免费本子（覆盖 ${new Set((r.items ?? []).map((i) => i.site)).size} 个源）`)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setGenerating(false)
    }
  }

  const reload = async () => {
    setLoading(true)
    setErr('')
    try {
      const [c, o, s] = await Promise.all([
        adminVip.listCodes(),
        adminVip.listOrders(),
        adminVip.settings(),
      ])
      setCodes(c.list ?? [])
      setOrders(o.list ?? [])
      setFreeComics(s.free_comics ?? '')
      setMonthPrice(s.month_price ?? '')
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }

  const syncOrders = async () => {
    setSyncing(true)
    setErr('')
    setMsg('')
    try {
      const r = await adminVip.syncOrders(1)
      setMsg(`对账完成：抓取 ${r.fetched} 单，新发放 ${r.created} 单`)
      await reload()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setSyncing(false)
    }
  }

  const testAfdian = async () => {
    setErr('')
    setMsg('')
    try {
      const r = await adminVip.testAfdian()
      if (r.ok) setMsg('爱发电连接正常：token 与签名有效')
      else setErr(`爱发电返回错误：ec=${r.ec} ${r.em ?? ''}`)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    void reload()
  }, [])

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    setErr('')
    setMsg('')
    try {
      await fn()
      setMsg(ok)
      await reload()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setMsg('已复制：' + text)
    } catch {
      setErr('复制失败，请手动选取卡密')
    }
  }

  if (loading && codes.length === 0 && orders.length === 0) return <CenterLoading />

  const pending = orders.filter((o) => !o.credited_at)

  return (
    <Box>
      {err && <Alert severity="error" sx={{ mb: 1 }} onClose={() => setErr('')}>{err}</Alert>}
      {msg && <Alert severity="success" sx={{ mb: 1 }} onClose={() => setMsg('')}>{msg}</Alert>}

      <SectionTitle>生成卡密</SectionTitle>
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} sx={{ mb: 1 }} alignItems="center">
        <TextField
          size="small" type="number" label="数量" value={count}
          onChange={(e) => setCount(Number(e.target.value))} sx={{ width: 110 }}
          inputProps={{ min: 1, max: 500 }}
        />
        <TextField
          size="small" type="number" label="有效天数" value={days}
          onChange={(e) => setDays(Number(e.target.value))} sx={{ width: 130 }}
          inputProps={{ min: 1, max: 3650 }}
        />
        <TextField
          size="small" label="备注（可选）" value={note}
          onChange={(e) => setNote(e.target.value)} sx={{ flex: 1, minWidth: 180 }}
        />
        <Button
          variant="contained"
          onClick={() => void run(() => adminVip.createCodes(count, days, note), `已生成 ${count} 张卡密`)}
        >
          生成
        </Button>
      </Stack>
      <Typography variant="caption" color="text.secondary">
        卡密为一次性凭证，用户可在「会员」页兑换；已使用的卡密不可重复兑换。
      </Typography>

      <Divider sx={{ my: 2 }} />

      <SectionTitle>手动发放 / 调整</SectionTitle>
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} sx={{ mb: 2 }} alignItems="center">
        <TextField
          size="small" label="用户名" value={grantName}
          onChange={(e) => setGrantName(e.target.value)} sx={{ width: 200 }}
        />
        <TextField
          size="small" type="number" label="追加天数" value={grantDays}
          onChange={(e) => setGrantDays(Number(e.target.value))} sx={{ width: 130 }}
          inputProps={{ min: 1, max: 3650 }}
        />
        <Button
          variant="outlined"
          disabled={!grantName.trim()}
          onClick={() =>
            void run(async () => {
              await adminVip.grant({ username: grantName.trim() }, grantDays)
              setGrantName('')
            }, '已发放 VIP')
          }
        >
          发放 VIP
        </Button>
      </Stack>

      <Divider sx={{ my: 2 }} />

      <SectionTitle>免费本子白名单</SectionTitle>
      <Typography variant="caption" color="text.secondary">
        非 VIP 用户仅可访问这些漫画（跨源用「站点:ID」表示）。留空表示非 VIP 无法访问任何正文。
      </Typography>
      <Stack direction="row" spacing={1} sx={{ mt: 1, mb: 1, flexWrap: 'wrap', alignItems: 'center' }}>
        <TextField
          size="small"
          type="number"
          label="数量"
          sx={{ width: 110 }}
          value={genCount}
          onChange={(e) => setGenCount(Math.max(1, Number(e.target.value) || 1))}
        />
        <Button size="small" variant="outlined" disabled={generating} onClick={() => void generateFree()}>
          {generating ? '生成中…' : '一键生成'}
        </Button>
        <Typography variant="caption" color="text.secondary">
          跨所有源挑选，白名单总数固定为上面的数量
        </Typography>
      </Stack>
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} sx={{ mt: 1, mb: 2 }} alignItems="flex-start">
        <TextField
          size="small" multiline minRows={2} fullWidth label="免费漫画 ID"
          value={freeComics} onChange={(e) => setFreeComics(e.target.value)}
          placeholder="例如 123456, 789012"
        />
        <Button
          variant="contained"
          onClick={() => void run(() => adminVip.setFreeComics(freeComics), '白名单已保存')}
        >
          保存
        </Button>
      </Stack>

      <Divider sx={{ my: 2 }} />

      <SectionTitle>会员定价</SectionTitle>
      <Typography variant="caption" color="text.secondary">
        设置月费后，爱发电订单按「实付 ÷ 月费」折算会员天数：只给整月，不足一个月的零头不发放。
        留空或填 0 表示不启用，回到按方案月数发放（旧行为）。兑换码／赠送订单实付为 0 元，不受此设置影响，仍按方案月数发放。
      </Typography>
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} sx={{ mt: 1, mb: 2 }} alignItems="flex-start">
        <TextField
          size="small" type="number" label="月费（元）" sx={{ width: 180 }}
          value={monthPrice} onChange={(e) => setMonthPrice(e.target.value)}
          placeholder="例如 30"
          helperText={monthPrice.trim() ? '不足一个月的金额将不发放' : '留空表示不按金额折算'}
        />
        <Button
          variant="contained"
          onClick={() =>
            void run(async () => {
              const r = await adminVip.setMonthPrice(monthPrice)
              setMonthPrice(r.month_price ?? '')
            }, '月费已保存')
          }
        >
          保存
        </Button>
      </Stack>

      <Divider sx={{ my: 2 }} />

      <SectionTitle>爱发电订单（待绑定 {pending.length}）</SectionTitle>
      <Stack direction="row" spacing={1} sx={{ mb: 1.5, flexWrap: 'wrap' }}>
        <Button size="small" variant="outlined" disabled={syncing} onClick={() => void syncOrders()}>
          {syncing ? '同步中…' : '同步爱发电订单'}
        </Button>
        <Button size="small" variant="outlined" onClick={() => void testAfdian()}>
          测试连接
        </Button>
      </Stack>
      {orders.length === 0 ? (
        <EmptyState text="暂无订单。需在服务端配置 MCCMS_AFDIAN_TOKEN 与回调地址。" />
      ) : (
        <Stack spacing={1} sx={{ mb: 2 }}>
          {orders.map((o) => (
            <Box
              key={o.order_id}
              sx={{ display: 'flex', gap: 1.5, alignItems: 'center', flexWrap: 'wrap', fontSize: 13, borderBottom: '1px solid', borderColor: 'divider', pb: 1 }}
            >
              <Box component="span" sx={{ fontWeight: 600 }}>{o.order_id}</Box>
              <Box component="span" sx={{ color: 'text.secondary' }}>
                {o.received_at?.slice(0, 19).replace('T', ' ')}
              </Box>
              <Box component="span">{o.plan_title || o.plan_id || '赞助'}</Box>
              <Box component="span">¥{o.amount || '0'}</Box>
              <Box component="span" sx={{ color: (o.days ?? 0) > 0 ? 'text.secondary' : 'warning.main' }}>
                {o.days ?? 0} 天
              </Box>
              {o.days_note && (
                <Box component="span" sx={{ color: 'text.secondary' }}>{o.days_note}</Box>
              )}
              {o.remark && <Box component="span" sx={{ color: 'text.secondary' }}>备注: {o.remark}</Box>}
              {o.credited_at ? (
                <Chip size="small" color="success" label={`已发放 · ${o.linked_name || ''}`} />
              ) : (
                <>
                  <Chip size="small" color="warning" label="待绑定" />
                  <Button size="small" onClick={() => { setBindFor(o); setBindName('') }}>绑定账号</Button>
                </>
              )}
            </Box>
          ))}
        </Stack>
      )}

      <Divider sx={{ my: 2 }} />

      <SectionTitle>卡密列表（{codes.length}）</SectionTitle>
      {codes.length === 0 ? (
        <EmptyState text="尚未生成卡密" />
      ) : (
        <Stack spacing={0.5}>
          {codes.map((c) => (
            <Box
              key={c.id}
              sx={{ display: 'flex', gap: 1.5, alignItems: 'center', flexWrap: 'wrap', fontSize: 13, borderBottom: '1px solid', borderColor: 'divider', pb: 0.75 }}
            >
              <Box component="span" sx={{ fontFamily: 'monospace', fontWeight: 600 }}>{c.code}</Box>
              <Chip size="small" variant="outlined" label={`${c.days} 天`} />
              {isCodeUsed(c) ? (
                <Chip size="small" color="default" label={`已用 · ${c.used_by_name || ''}`} />
              ) : c.revoked ? (
                <Chip size="small" color="error" label="已作废" />
              ) : (
                <Chip size="small" color="success" label="未使用" />
              )}
              {c.note && <Box component="span" sx={{ color: 'text.secondary' }}>{c.note}</Box>}
              <Box sx={{ flex: 1 }} />
              <Tooltip title="复制">
                <IconButton size="small" onClick={() => void copy(c.code)}>
                  <ContentCopyIcon fontSize="inherit" />
                </IconButton>
              </Tooltip>
              {!isCodeUsed(c) && !c.revoked && (
                <Tooltip title="作废">
                  <IconButton
                    size="small"
                    onClick={() => void run(() => adminVip.revokeCode(c.id), '卡密已作废')}
                  >
                    <BlockIcon fontSize="inherit" />
                  </IconButton>
                </Tooltip>
              )}
              <Tooltip title="删除记录">
                <IconButton
                  size="small"
                  onClick={() => void run(() => adminVip.deleteCode(c.id), '已删除卡密')}
                >
                  <DeleteOutlineIcon fontSize="inherit" />
                </IconButton>
              </Tooltip>
            </Box>
          ))}
        </Stack>
      )}

      <Dialog open={!!bindFor} onClose={() => setBindFor(null)}>
        <DialogTitle>绑定爱发电订单</DialogTitle>
        <DialogContent>
          <Typography variant="body2" sx={{ mb: 2 }}>
            订单 {bindFor?.order_id}（{bindFor?.days ?? 0} 天）。填入本站用户名以发放权益。
          </Typography>
          {bindFor?.days_note && (
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 2 }}>
              折算说明：{bindFor.days_note}
            </Typography>
          )}
          {(bindFor?.days ?? 0) <= 0 && (
            <Alert severity="warning" sx={{ mb: 2 }}>
              该订单折算后不足一个月，绑定也不会发放权益。可改用上方的「手动发放」。
            </Alert>
          )}
          <TextField
            autoFocus fullWidth size="small" label="本站用户名"
            value={bindName} onChange={(e) => setBindName(e.target.value)}
          />
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setBindFor(null)}>取消</Button>
          <Button
            variant="contained"
            disabled={!bindName.trim() || (bindFor?.days ?? 0) <= 0}
            onClick={() => {
              const o = bindFor
              setBindFor(null)
              if (o) void run(() => adminVip.bindOrder(o.order_id, { username: bindName.trim() }), '订单已绑定并发放权益')
            }}
          >
            绑定并发放
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}