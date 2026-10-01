// 管理后台 · 站点设置：站点名称、关于、公告。
// 保存后调用 siteConfig.refresh()，全站（页头、标题、关于页、公告）立即生效。
import { useEffect, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Divider from '@mui/material/Divider'
import Stack from '@mui/material/Stack'
import Switch from '@mui/material/Switch'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { CenterLoading, SectionTitle } from '../components'
import { useSiteConfig } from '../siteConfig'
import { siteAdmin } from '../siteAdmin'

export default function AdminSite() {
  const { refresh } = useSiteConfig()
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const [name, setName] = useState('')
  const [about, setAbout] = useState('')
  const [annEnabled, setAnnEnabled] = useState(false)
  const [annTitle, setAnnTitle] = useState('')
  const [annContent, setAnnContent] = useState('')

  useEffect(() => {
    let alive = true
    void (async () => {
      try {
        const d = await siteAdmin.get()
        if (!alive) return
        setName(d.name ?? '')
        setAbout(d.about ?? '')
        setAnnEnabled(Boolean(d.announcement?.enabled))
        setAnnTitle(d.announcement?.title ?? '')
        setAnnContent(d.announcement?.content ?? '')
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : String(e))
      } finally {
        if (alive) setLoading(false)
      }
    })()
    return () => {
      alive = false
    }
  }, [])

  const save = async () => {
    setSaving(true)
    setErr('')
    setMsg('')
    try {
      await siteAdmin.save({
        name,
        about,
        announcement: { enabled: annEnabled, title: annTitle, content: annContent },
      })
      await refresh()
      setMsg('已保存，全站已生效')
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <CenterLoading />

  return (
    <Box>
      <SectionTitle>站点设置</SectionTitle>
      {err && <Alert severity="error" sx={{ mb: 1 }} onClose={() => setErr('')}>{err}</Alert>}
      {msg && <Alert severity="success" sx={{ mb: 1 }} onClose={() => setMsg('')}>{msg}</Alert>}

      <Stack spacing={2} sx={{ maxWidth: 720 }}>
        <TextField
          size="small"
          label="站点名称"
          value={name}
          onChange={(e) => setName(e.target.value)}
          helperText="显示在浏览器标题、页头与关于页"
        />
        <TextField
          size="small"
          multiline
          minRows={4}
          label="关于内容"
          value={about}
          onChange={(e) => setAbout(e.target.value)}
          helperText="关于页正文，支持换行"
        />

        <Divider />

        <Stack direction="row" spacing={1} alignItems="center">
          <Switch checked={annEnabled} onChange={(e) => setAnnEnabled(e.target.checked)} />
          <Typography variant="body2">启用首页公告</Typography>
        </Stack>
        <TextField
          size="small"
          label="公告标题"
          value={annTitle}
          onChange={(e) => setAnnTitle(e.target.value)}
          disabled={!annEnabled}
        />
        <TextField
          size="small"
          multiline
          minRows={3}
          label="公告内容"
          value={annContent}
          onChange={(e) => setAnnContent(e.target.value)}
          disabled={!annEnabled}
          helperText="用户首次访问时弹出，关闭后不再重复；内容变更会再次弹出"
        />

        <Stack direction="row">
          <Button variant="contained" disabled={saving} onClick={() => void save()}>
            {saving ? '保存中…' : '保存'}
          </Button>
        </Stack>
      </Stack>
    </Box>
  )
}