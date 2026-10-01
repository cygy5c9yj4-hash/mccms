// 关于：面向 C 端的极简介绍。捐助入口已统一收敛到「会员」页。
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import CardContent from '@mui/material/CardContent'
import Divider from '@mui/material/Divider'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import FavoriteIcon from '@mui/icons-material/Favorite'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import { SectionTitle } from '../components'
import { BRAND_GRADIENT } from '../theme'
import { useSiteConfig } from '../siteConfig'

export default function About() {
  const { name, about } = useSiteConfig()

  return (
    <Box sx={{ maxWidth: 720, mx: 'auto' }}>
      <SectionTitle>
        <InfoOutlinedIcon sx={{ color: 'primary.main' }} /> 关于
      </SectionTitle>

      <Card sx={{ borderRadius: 5 }}>
        <CardContent sx={{ p: { xs: 3, md: 4 } }}>
          <Typography variant="h4" sx={{ fontWeight: 700, letterSpacing: '-0.02em', mb: 2 }}>
            <Box
              component="span"
              sx={{
                background: BRAND_GRADIENT,
                WebkitBackgroundClip: 'text',
                backgroundClip: 'text',
                color: 'transparent',
              }}
            >
              {name}
            </Box>
          </Typography>

          {about ? (
            <Typography color="text.secondary" sx={{ lineHeight: 1.9, whiteSpace: 'pre-wrap' }}>
              {about}
            </Typography>
          ) : (
            <Typography color="text.secondary" sx={{ lineHeight: 1.9 }}>
              {name} 是一个漫画在线阅读与下载平台，内容来自公开来源，
              本站只做整理与阅读，不存储任何作品文件。
            </Typography>
          )}

          <Divider sx={{ my: 3 }} />

          <Stack direction="row" spacing={1.5} alignItems="flex-start">
            <FavoriteIcon color="error" fontSize="small" sx={{ mt: 0.4 }} />
            <Typography sx={{ lineHeight: 1.9 }}>
              也许有一天它会关闭，但
              <Box component="span" sx={{ fontWeight: 700 }}>
                永远不会变质
              </Box>
              ：我会努力保持无广告喵！
            </Typography>
          </Stack>
        </CardContent>
      </Card>

      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', textAlign: 'center', mt: 3 }}>
        {name}
      </Typography>
    </Box>
  )
}