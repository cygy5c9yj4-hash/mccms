import { lazy, Suspense, useEffect, useState } from 'react'
import { useSiteConfig } from '../siteConfig'

// 首访公告（薄壳）。
// 内容来自后台「站点设置」，不再单独请求：站点配置上下文已经拉过一次。
// 真正重的 Markdown 渲染拆到 AnnouncementDialog.tsx（独立 chunk），不计入主包。
// 这里不 import 任何 MUI 组件：无可展示内容时本组件零成本（返回 null）。

const AnnouncementDialog = lazy(() => import('./AnnouncementDialog'))

// 存「已读公告内容的哈希」：公告更新时才再弹一次。
const DISMISS_KEY = 'jm_aura_announcement_dismissed'

// 轻量内容哈希，避免引入 crypto 依赖。
function hashText(s: string): string {
  let h = 5381
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0
  return `a${(h >>> 0).toString(36)}-${s.length.toString(36)}`
}

export default function Announcement() {
  const { announcement } = useSiteConfig()
  const [text, setText] = useState('')
  const [hash, setHash] = useState('')

  useEffect(() => {
    try {
      if (!announcement.enabled) {
        setText('')
        return
      }
      const body = announcement.content.trim()
      if (!body) {
        setText('')
        return
      }
      const composed = announcement.title ? `## ${announcement.title}\n\n${body}` : body
      const h = hashText(composed)
      if (localStorage.getItem(DISMISS_KEY) === h) return
      setText(composed)
      setHash(h)
    } catch {
      /* localStorage 可能被禁用：静默忽略 */
    }
  }, [announcement.enabled, announcement.title, announcement.content])

  if (!text) return null

  const dismiss = () => {
    try {
      localStorage.setItem(DISMISS_KEY, hash)
    } catch {
      /* ignore */
    }
    setText('')
  }

  return (
    <Suspense fallback={null}>
      <AnnouncementDialog content={text} onDismiss={dismiss} />
    </Suspense>
  )
}