// 阅读历史：本地账号历史（/api/me/history）。替代原 JM-Aura 云端 library。
import { useState } from 'react'
import { Link as RouterLink } from 'react-router-dom'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Divider from '@mui/material/Divider'
import IconButton from '@mui/material/IconButton'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import ListItemAvatar from '@mui/material/ListItemAvatar'
import Avatar from '@mui/material/Avatar'
import ListItemText from '@mui/material/ListItemText'
import Stack from '@mui/material/Stack'
import HistoryIcon from '@mui/icons-material/History'
import MenuBookIcon from '@mui/icons-material/MenuBook'
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline'
import { account } from '../account'
import { CenterLoading, EmptyState, SectionTitle, useAsync } from '../components'
import { useAuth } from '../auth'

export default function AuraHistory() {
  const { user, loading } = useAuth()
  const [tick, setTick] = useState(0)
  const state = useAsync(
    async () => (user ? (await account.listHistory()).list : []),
    [user?.username, tick],
  )
  const reload = () => setTick((t) => t + 1)

  if (loading) return <CenterLoading />
  if (!user) {
    return (
      <EmptyState
        icon={<HistoryIcon />}
        text="登录后查看阅读历史"
        action={
          <Button component={RouterLink} to="/login" variant="contained">
            去登录
          </Button>
        }
      />
    )
  }
  const items = state.data ?? []
  return (
    <Box>
      <SectionTitle>阅读历史</SectionTitle>
      {state.loading ? (
        <CenterLoading />
      ) : items.length === 0 ? (
        <EmptyState icon={<HistoryIcon />} text="还没有阅读记录" />
      ) : (
        <>
          <Stack direction="row" justifyContent="flex-end" sx={{ mb: 1 }}>
            <Button
              size="small"
              color="error"
              startIcon={<DeleteOutlineIcon />}
              onClick={() => void account.clearHistory().then(reload)}
            >
              清空历史
            </Button>
          </Stack>
          <List>
            {items.map((h) => (
              <Box key={`${h.source}:${h.comic_id}`}>
                <ListItem
                  secondaryAction={
                    <IconButton
                      edge="end"
                      aria-label="删除记录"
                      onClick={() => void account.removeHistory(h.source, h.comic_id).then(reload)}
                    >
                      <DeleteOutlineIcon />
                    </IconButton>
                  }
                >
                  <ListItemAvatar>
                    <Avatar variant="rounded" src={h.cover_url} sx={{ width: 56, height: 74 }}>
                      <MenuBookIcon />
                    </Avatar>
                  </ListItemAvatar>
                  <ListItemText
                    primary={
                      <Box
                        component={RouterLink}
                        to={`/comic/${encodeURIComponent(h.comic_id)}?site=${encodeURIComponent(h.source)}`}
                        sx={{ color: 'inherit', textDecoration: 'none', fontWeight: 600 }}
                      >
                        {h.title}
                      </Box>
                    }
                    secondary={
                      <>
                        {h.chapter_title || h.chapter_id}
                        {h.page > 0 ? ` · 已读到第 ${h.page + 1} 页` : ''}
                        <br />
                        <Box component="span" sx={{ color: 'text.disabled' }}>
                          {h.source}
                          {h.updated_at ? ` · ${h.updated_at}` : ''}
                        </Box>
                      </>
                    }
                  />
                </ListItem>
                <Divider variant="inset" component="li" />
              </Box>
            ))}
          </List>
        </>
      )}
    </Box>
  )
}