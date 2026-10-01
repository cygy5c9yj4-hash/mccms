// 站点外观/文案配置：站点名称、关于、公告。
// 全部由管理员在后台设置（后端 /api/site），前端只负责展示，不硬编码。
//
// 设计要点：
// - 只请求一次，全局共享；管理面板改完保存后调用 refresh() 立即生效。
// - 名称同时写入 document.title，保证浏览器标签页也是自定义名称。

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { api } from './api'

export interface Announcement {
  enabled: boolean
  title: string
  content: string
}

export interface SiteConfig {
  name: string
  about: string
  announcement: Announcement
}

export const DEFAULT_SITE_NAME = '漫画阅读器'

const DEFAULT_CONFIG: SiteConfig = {
  name: DEFAULT_SITE_NAME,
  about: '',
  announcement: { enabled: false, title: '', content: '' },
}

interface SiteConfigCtx extends SiteConfig {
  loaded: boolean
  refresh: () => Promise<void>
}

const Ctx = createContext<SiteConfigCtx>({
  ...DEFAULT_CONFIG,
  loaded: false,
  refresh: async () => {},
})

function normalize(input: Partial<SiteConfig> | undefined | null): SiteConfig {
  if (!input) return DEFAULT_CONFIG
  const a = input.announcement
  return {
    name: (input.name || '').trim() || DEFAULT_SITE_NAME,
    about: input.about ?? '',
    announcement: {
      enabled: Boolean(a?.enabled),
      title: (a?.title ?? '').trim(),
      content: (a?.content ?? '').trim(),
    },
  }
}

export function SiteConfigProvider({ children }: { children: ReactNode }) {
  const [config, setConfig] = useState<SiteConfig>(DEFAULT_CONFIG)
  const [loaded, setLoaded] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const d = await api.get<Partial<SiteConfig>>('/api/site')
      setConfig(normalize(d))
    } catch {
      /* 失败时保留默认值，不影响站点可用性 */
    } finally {
      setLoaded(true)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    if (config.name) document.title = config.name
  }, [config.name])

  const value = useMemo<SiteConfigCtx>(
    () => ({ ...config, loaded, refresh }),
    [config, loaded, refresh],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useSiteConfig() {
  return useContext(Ctx)
}