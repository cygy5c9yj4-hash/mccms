// 站点外观设置（名称 / 关于 / 公告）的后台接口。
import { api } from './api'
import type { SiteConfig } from './siteConfig'

export const siteAdmin = {
  get: () => api.get<SiteConfig>('/api/admin/site'),
  save: (body: Partial<SiteConfig>) => api.post<SiteConfig>('/api/admin/site', body),
}