// 站点上下文：本后端同时支持多个站点，所有请求都要带上当前站点。
//
// 站点选择持久化在 localStorage，并同步注入到每个 API 请求（见 api.ts）。

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { api } from './api'

export interface SiteInfo {
  key: string
  name: string
  domains: string[]
  capabilities: Record<string, boolean>
}

interface SiteCtx {
  site: string
  sites: SiteInfo[]
  current?: SiteInfo
  setSite: (key: string) => void
}

const STORAGE_KEY = 'mccms.site'
const DEFAULT_SITE = 's1'

const SiteContext = createContext<SiteCtx>({
  site: DEFAULT_SITE,
  sites: [],
  setSite: () => {},
})

/** 供 api.ts 读取当前站点（避免循环依赖） */
export function currentSiteKey(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) || DEFAULT_SITE
  } catch {
    return DEFAULT_SITE
  }
}

export function SiteProvider({ children }: { children: ReactNode }) {
  const [site, setSiteState] = useState<string>(currentSiteKey)
  const [sites, setSites] = useState<SiteInfo[]>([])

  useEffect(() => {
    let alive = true
    api
      .get<{ sites: SiteInfo[] }>('/api/sites')
      .then((d) => {
        if (!alive) return
        setSites(d.sites ?? [])
        // 本地缓存的站点若已不存在，回落到第一个
        if (d.sites?.length && !d.sites.some((s) => s.key === currentSiteKey())) {
          setSite(d.sites[0].key)
        }
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

  const setSite = useCallback((key: string) => {
    try {
      localStorage.setItem(STORAGE_KEY, key)
    } catch {
      /* 隐私模式忽略 */
    }
    setSiteState(key)
    // 切换站点后整页刷新，避免各页面缓存了上一站点的数据
    window.location.reload()
  }, [])

  const current = useMemo(() => sites.find((s) => s.key === site), [sites, site])

  return (
    <SiteContext.Provider value={{ site, sites, current, setSite }}>{children}</SiteContext.Provider>
  )
}

export function useSite() {
  return useContext(SiteContext)
}
