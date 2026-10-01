// 本地账号系统 API —— 独立于各站点的 JM 账号透传层。
// 契约与 mccms-go/internal/web/auth.go 一一对应。
import { api } from './api';

export interface PublicUser {
  id: string;
  username: string;
  display_name?: string;
  email?: string;
  role: 'admin' | 'user';
  status: 'active' | 'disabled';
  tier: 'free' | 'vip' | string;
  is_admin: boolean;
  created_at?: string;
  last_login_at?: string | null;
}

export interface Folder {
  id: string;
  name: string;
  created_at?: string;
}

export interface FavoriteItem {
  user_id?: string;
  folder_id?: string;
  source: string;
  comic_id: string;
  title?: string;
  author?: string;
  cover_url?: string;
  tags?: string[];
  kind?: string;
  created_at?: string;
}

export interface HistoryItem {
  source: string;
  comic_id: string;
  title?: string;
  cover_url?: string;
  kind?: string;
  chapter_id?: string;
  chapter_title?: string;
  page: number;
  updated_at?: string;
}

export interface NoteItem {
  id: string;
  source: string;
  comic_id: string;
  comic_title?: string;
  kind?: string;
  body: string;
  created_at?: string;
  updated_at?: string;
}

export interface AuditItem {
  id: string;
  at: string;
  actor_id?: string;
  actor_name: string;
  action: string;
  target_id?: string;
  target?: string;
  detail?: string;
  ip?: string;
}

export interface AdminCounts {
  users: number;
  admins: number;
  disabled_users: number;
  sessions: number;
  folders: number;
  favorites: number;
  history: number;
  notes: number;
}

export interface AdminOverview {
  counts: AdminCounts;
  runtime: Record<string, unknown>;
  settings: { registration_open: boolean };
}

export interface MeInfo {
  user: PublicUser | null;
  registration_open: boolean;
  bootstrap: boolean; // 没有任何用户：首个注册者将成为管理员
}

export const account = {
  register(username: string, password: string) {
    return api.post<{ user: PublicUser }>('/api/auth/register', { username, password });
  },
  login(username: string, password: string) {
    return api.post<{ user: PublicUser }>('/api/auth/login', { username, password });
  },
  logout() {
    return api.post<{ logged_out: boolean }>('/api/auth/logout', {});
  },
  /** 未登录时抛错（st=1014）；需要匿名态信息用 meInfo。 */
  me(): Promise<PublicUser> {
    return api.get<PublicUser>('/api/auth/me').then((d) => {
      const info = d as unknown as MeInfo;
      return info.user ?? (d as unknown as PublicUser);
    });
  },
  /** 永不抛「未登录」：返回匿名态信息。 */
  meInfo(): Promise<MeInfo> {
    return api.get<MeInfo>('/api/auth/me');
  },
  changePassword(oldPassword: string, newPassword: string) {
    return api.post<unknown>('/api/auth/password', {
      old_password: oldPassword,
      new_password: newPassword,
    });
  },

  // 收藏夹分组
  listFolders: () => api.get<{ list: Folder[] }>('/api/me/favorite-folders'),
  createFolder: (name: string) => api.post<unknown>('/api/me/favorite-folders', { name }),
  deleteFolder: (id: string) =>
    api.del<unknown>(`/api/me/favorite-folders?id=${encodeURIComponent(id)}`),

  // 收藏
  listFavorites: (folderId?: string) =>
    api.get<{ list: FavoriteItem[] }>(
      `/api/me/favorites${folderId ? `?folder_id=${encodeURIComponent(folderId)}` : ''}`,
    ),
  checkFavorite: (source: string, comicId: string) =>
    api.get<{ is_favorite: boolean }>(
      `/api/me/favorite-check?source=${encodeURIComponent(source)}&comic_id=${encodeURIComponent(comicId)}`,
    ),
  addFavorite(item: {
    source: string;
    comic_id: string;
    title?: string;
    author?: string;
    cover_url?: string;
    folder_id?: string;
    kind?: string;
    tags?: string[];
  }) {
    return api.post<unknown>('/api/me/favorites', item);
  },
  removeFavorite: (source: string, comicId: string) =>
    api.del<unknown>(
      `/api/me/favorites?source=${encodeURIComponent(source)}&comic_id=${encodeURIComponent(comicId)}`,
    ),
  /** 移动收藏夹 = 删除后按新 folder_id 重加。 */
  moveFavorite: async (item: FavoriteItem, folderId: string) => {
    await account.removeFavorite(item.source, item.comic_id);
    await account.addFavorite({ ...item, folder_id: folderId });
  },

  // 历史
  listHistory: () => api.get<{ list: HistoryItem[]; total: number }>('/api/me/history'),
  recordHistory(item: {
    source: string;
    comic_id: string;
    title?: string;
    cover_url?: string;
    kind?: string;
    chapter_id?: string;
    chapter_title?: string;
    page: number;
  }) {
    return api.post<unknown>('/api/me/history', item);
  },
  removeHistory: (source: string, comicId: string) =>
    api.del<unknown>(
      `/api/me/history?source=${encodeURIComponent(source)}&comic_id=${encodeURIComponent(comicId)}`,
    ),
  clearHistory: () => api.del<unknown>('/api/me/history?all=1'),

  // 笔记
  listNotes: (source?: string, comicId?: string) =>
    api.get<{ list: NoteItem[]; total: number }>(
      `/api/me/notes${source && comicId ? `?source=${encodeURIComponent(source)}&comic_id=${encodeURIComponent(comicId)}` : ''}`,
    ),
  saveNote: (item: {
    source: string;
    comic_id: string;
    comic_title?: string;
    kind?: string;
    body: string;
  }) => api.post<{ note: NoteItem }>('/api/me/notes', item),
  updateNote: (id: string, body: string) =>
    api.put<{ note: NoteItem }>('/api/me/notes', { id, body }),
  removeNote: (id: string) =>
    api.del<unknown>(`/api/me/notes?id=${encodeURIComponent(id)}`),

  // 管理后台
  admin: {
    overview: () => api.get<AdminOverview>('/api/admin/overview'),
    listUsers: () => api.get<{ list: PublicUser[]; total: number }>('/api/admin/users'),
    audit: () => api.get<{ list: AuditItem[] }>('/api/admin/audit'),
    settings: () => api.get<{ registration_open: boolean }>('/api/admin/settings'),
    setRegistrationOpen: (open: boolean) =>
      api.post<unknown>('/api/admin/settings', { registration_open: open }),
    setStatus: (userId: string, status: 'active' | 'disabled') =>
      api.post<unknown>('/api/admin/users/status', { user_id: userId, status }),
    setRole: (userId: string, role: 'admin' | 'user') =>
      api.post<unknown>('/api/admin/users/role', { user_id: userId, role }),
    setTier: (userId: string, tier: 'free' | 'vip', expiresAt?: string) =>
      api.post<unknown>('/api/admin/users/tier', {
        user_id: userId,
        tier,
        expires_at: expiresAt ?? null,
      }),
    resetPassword: (userId: string, password: string) =>
      api.post<unknown>('/api/admin/users/password', { user_id: userId, password }),

    revokeSessions: (userId: string) =>
      api.post<unknown>('/api/admin/users/revoke-sessions', { user_id: userId }),
    deleteUser: (userId: string) =>
      api.del<unknown>(`/api/admin/users?user_id=${encodeURIComponent(userId)}`),
  },
};