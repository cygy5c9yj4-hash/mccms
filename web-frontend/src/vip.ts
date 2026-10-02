// VIP / 卡密 / 爱发电订单相关接口。
import { api } from './api'

export interface VipStatus {
  tier: 'free' | 'vip'
  active: boolean
  username?: string
  expires_at?: string | null
  days_left?: number
  free_comics: string[]
  /** 会员月费（元）。空串表示未设置，此时按爱发电方案月数发放。 */
  month_price?: string
}

export interface RedeemCode {
  id: string
  code: string
  note?: string
  days: number
  batch?: string
  created_at: string
  created_by?: string
  used_by?: string
  used_by_name?: string
  used_at?: string | null
  revoked?: boolean
}

export interface AfdianOrder {
  order_id: string
  out_trade_no?: string
  afdian_user_id?: string
  user_private_id?: string
  custom_order_id?: string
  redeem_id?: string
  plan_id?: string
  plan_title?: string
  months?: number
  days?: number
  /** 天数折算说明：按金额折算 / 按方案月数 / 金额不足未发放。 */
  days_note?: string
  amount?: string
  remark?: string
  status: number
  linked_user_id?: string
  linked_name?: string
  linked_at?: string | null
  credited_at?: string | null
  received_at: string
}

// 爱发电账号绑定状态。
export interface AfdianBinding {
  bound: boolean
  masked_id?: string
  bound_at?: string | null
}

export const vip = {
  status: () => api.get<VipStatus>('/api/vip/status'),
  redeem: (code: string) =>
    api.post<{ tier: string; days: number; expires_at: string }>('/api/vip/redeem', { code }),
  afdian: () => api.get<AfdianBinding>('/api/me/afdian'),
  unbindAfdian: () => api.post<{ bound: boolean }>('/api/me/afdian/unbind', {}),

  /** 付款时漏填「留言」的用户，可凭爱发电订单号自助归户并立即开通。 */
  claim: (orderId: string) =>
    api.post<{
      tier: string
      claimed: boolean
      days: number
      username?: string
      expires_at?: string | null
    }>('/api/vip/claim', { order_id: orderId }),
}

export const adminVip = {
  listCodes: () => api.get<{ list: RedeemCode[]; total: number }>('/api/admin/vip/codes'),
  createCodes: (count: number, days: number, note: string) =>
    api.post<{ list: RedeemCode[]; count: number }>('/api/admin/vip/codes', { count, days, note }),
  revokeCode: (id: string) =>
    api.post<unknown>(`/api/admin/vip/codes/${encodeURIComponent(id)}`, {}),
  deleteCode: (id: string) =>
    api.del<unknown>(`/api/admin/vip/codes/${encodeURIComponent(id)}`),
  listOrders: () => api.get<{ list: AfdianOrder[]; total: number }>('/api/admin/vip/orders'),
  bindOrder: (orderId: string, target: { user_id?: string; username?: string }) =>
    api.post<unknown>(`/api/admin/vip/orders/${encodeURIComponent(orderId)}/bind`, target),
  grant: (target: { username: string }, days: number) =>
    api.post<unknown>('/api/admin/vip/grant', { ...target, days }),
  settings: () => api.get<{ free_comics: string; month_price: string }>('/api/admin/vip/settings'),
  syncOrders: (pages = 1) =>
    api.post<{ ok: boolean; fetched: number; created: number }>(
      `/api/admin/vip/sync?pages=${pages}`,
      {},
    ),
  testAfdian: () =>
    api.post<{ ok: boolean; ec: number; em?: string }>('/api/admin/vip/ping', {}),

  /** 一键生成免费白名单：跨多个源挑选，总量固定为 count（整体替换）。 */
  generateFreeComics: (count: number, sites: string[]) =>
    api.post<{
      count: number
      items: { site: string; id: string; name: string }[]
      free_comics: string[]
    }>('/api/admin/vip/free-comics/generate', { count, sites }),

  setFreeComics: (freeComics: string) =>
    api.post<{ free_comics: string }>('/api/admin/vip/settings', { free_comics: freeComics }),

  /** 设置会员月费（元）。传空串或 0 表示停用按金额折算，回到按方案月数发放。 */
  setMonthPrice: (monthPrice: string) =>
    api.post<{ month_price: string }>('/api/admin/vip/settings', { month_price: monthPrice }),
}

export function isCodeUsed(c: RedeemCode): boolean {
  return !!c.used_at
}
