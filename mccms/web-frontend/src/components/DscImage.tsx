import { memo, useCallback, useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import BrokenImageIcon from '@mui/icons-material/BrokenImage'
import { md5 } from '../md5'

// 移植自旧版 Vue DescrambledImage：JM 图片切片乱序还原。
// 算法与阈值（220980 / 268850 / 421926）必须与后端解码及上游实现完全一致。

function isGif(url: string): boolean {
  return url.toLowerCase().includes('.gif')
}

export function getSegmentationNum(
  epsId: string,
  scrambleId: string,
  pictureName: string,
): number {
  const sid = parseInt(scrambleId, 10) || 220980
  const eid = parseInt(epsId, 10)
  if (isNaN(eid)) return 0
  if (eid < sid) return 0
  if (eid < 268850) return 10
  const keyCode = md5(String(eid) + String(pictureName)).charCodeAt(32 - 1)
  if (eid > 421926) {
    return (keyCode % 8) * 2 + 2
  }
  return (keyCode % 10) * 2 + 2
}

interface DscImageProps {
  src: string
  comicId: string
  scrambleId: string
  index?: number
  /** 超过该序号的图片进入视口后才真正加载（阅读器性能门控） */
  lazyAfter?: number
  /** 阅读器内解除 900px 上限，跟随容器宽度设置 */
  fullWidth?: boolean
}

type Phase = 'idle' | 'loading' | 'error' | 'done'

function DscImage({ src, comicId, scrambleId, index = 0, lazyAfter, fullWidth }: DscImageProps) {
  const [phase, setPhase] = useState<Phase>('idle')
  const [displaySrc, setDisplaySrc] = useState('')
  const [canvasEl, setCanvasEl] = useState<HTMLCanvasElement | null>(null)
  const [imgKey, setImgKey] = useState(0)
  const [visible, setVisible] = useState(false)
  // 原图尺寸：用于生成与真实渲染等高的 aspect-ratio 占位，加载/切页时滚动几何完全不变。
  const [natW, setNatW] = useState(0)
  const [natH, setNatH] = useState(0)

  const holderRef = useRef<HTMLDivElement | null>(null)
  const canvasContainerRef = useRef<HTMLDivElement | null>(null)
  const loadTokenRef = useRef(0)
  const retriesRef = useRef(0)

  const needDescramble = !isGif(src) && scrambleId !== '0'

  // 懒加载门控：仅当 index 超出预载窗口时才等待可见性
  useEffect(() => {
    if (!lazyAfter || index < lazyAfter) {
      setVisible(true)
      return
    }
    const el = holderRef.current
    if (!el || typeof IntersectionObserver === 'undefined') {
      setVisible(true)
      return
    }
    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setVisible(true)
            io.disconnect()
          }
        }
      },
      { rootMargin: '600px 0px' },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [lazyAfter, index])

  // 临近区探测已移除：解码后的位图保留到切页/卸载，回滚零重解码、零闪烁。

  const cutImage = useCallback(
    (image: HTMLImageElement, token: number): boolean => {
      try {
        const width = image.naturalWidth
        const height = image.naturalHeight
        const pictureName = src.substring(src.lastIndexOf('/') + 1).split('?')[0].split('.')[0]
        const sliceCount = getSegmentationNum(comicId, scrambleId, pictureName)
        if (!width || !height || sliceCount <= 1 || height < sliceCount * 2) {
          setDisplaySrc(src)
          setCanvasEl(null)
          return true
        }

        const canvas = document.createElement('canvas')
        canvas.width = width
        canvas.height = height
        const context = canvas.getContext('2d')
        if (!context) {
          setDisplaySrc(src)
          setCanvasEl(null)
          return true
        }

        // 与旧版一致：先构建各块的 [startY, endY]，再从最后一块向前依次绘制
        const rem = height % sliceCount
        const copyHeight = Math.floor(height / sliceCount)
        const blocks: Array<[number, number]> = []
        let totalH = 0
        for (let i = 0; i < sliceCount; i++) {
          let h = copyHeight * (i + 1)
          if (i === sliceCount - 1) {
            h += rem
          }
          blocks.push([totalH, h])
          totalH = h
        }

        let destY = 0
        for (let i = blocks.length - 1; i >= 0; i--) {
          const start = blocks[i][0]
          const end = blocks[i][1]
          const sliceH = end - start
          context.drawImage(image, 0, start, width, sliceH, 0, destY, width, sliceH)
          destY += sliceH
        }

        if (loadTokenRef.current !== token) return false
        // 直接挂载 canvas 元素，跳过 toDataURL 重编码，实现无损显示（兼容所有浏览器）
        setCanvasEl(canvas)
        setDisplaySrc('')
        return true
      } catch {
        return false
      }
    },
    [src, comicId, scrambleId],
  )

  useEffect(() => {
    if (phase === 'idle' && visible) {
      setPhase('loading')
    }
  }, [phase, visible])

  useEffect(() => {
    if (phase !== 'loading') return
    const token = ++loadTokenRef.current

    const url = imgKey > 0 ? `${src}${src.includes('?') ? '&' : '?'}retry=${imgKey}` : src
    const image = new Image()
    // 仅跨域时需要 CORS 许可才能安全读取画布；同源代理无需该头
    if (/^https?:\/\//i.test(url) && !url.startsWith(window.location.origin)) {
      image.crossOrigin = 'anonymous'
    }
    image.decoding = 'async'

    const cleanup = () => {
      image.onload = null
      image.onerror = null
    }

    image.onload = () => {
      cleanup()
      if (loadTokenRef.current !== token) return
      const w = image.naturalWidth || 0
      const h = image.naturalHeight || 0
      if (w > 0 && h > 0) {
        setNatW(w)
        setNatH(h)
      }
      if (!needDescramble) {
        setDisplaySrc(url)
        setCanvasEl(null)
        setPhase('done')
        return
      }
      if (cutImage(image, token)) {
        setPhase('done')
      } else if (loadTokenRef.current === token) {
        if (retriesRef.current < 3) {
          retriesRef.current += 1
          setImgKey((k) => k + 1)
        } else {
          setPhase('error')
        }
      }
    }
    image.onerror = () => {
      cleanup()
      if (loadTokenRef.current !== token) return
      if (retriesRef.current < 3) {
        retriesRef.current += 1
        setImgKey((k) => k + 1)
      } else {
        setPhase('error')
      }
    }
    image.src = url
    return cleanup
  }, [phase, visible, imgKey, src, needDescramble, cutImage])

  // 组件卸载或换页时使进行中的任务失效
  useEffect(() => {
    return () => {
      loadTokenRef.current += 1
    }
  }, [])

  // 将 descramble 后的 canvas 元素直接挂载到容器，无损显示
  useEffect(() => {
    const container = canvasContainerRef.current
    if (!container || !canvasEl) return
    container.innerHTML = ''
    canvasEl.style.width = '100%'
    canvasEl.style.maxWidth = fullWidth ? 'none' : '900px'
    canvasEl.style.margin = '0 auto'
    canvasEl.style.display = 'block'
    canvasEl.style.userSelect = 'none'
    canvasEl.style.webkitUserSelect = 'none'
    container.appendChild(canvasEl)
    return () => {
      if (canvasEl.parentElement === container) container.removeChild(canvasEl)
    }
  }, [canvasEl, fullWidth])

  const retry = () => {
    retriesRef.current = 0
    setImgKey(0)
    setDisplaySrc('')
    setCanvasEl(null)
    setPhase('idle')
  }

  // 已解码/已加载才展示实体；否则用等高占位顶住布局
  const hasBox = natW > 0 && natH > 0
  const showContent = phase === 'done' && Boolean(canvasEl || displaySrc)

  return (
    <Box
      ref={holderRef}
      sx={{
        position: 'relative',
        // 必须给 holder 确定宽度：父容器是 flex + justify-content:center，若此处为 auto，
        // 内部 width:100% 的等比占位会因宽度未定而塌陷成 0 高，造成占位与实体切换时高度突变、滚动跳顶。
        width: '100%',
        maxWidth: fullWidth ? 'none' : 900,
        mx: 'auto',
        minHeight: showContent || hasBox ? undefined : '40vh',
      }}
    >
      {showContent ? (
        canvasEl ? (
          <Box
            ref={canvasContainerRef}
            sx={{ width: '100%', aspectRatio: hasBox ? `${natW} / ${natH}` : undefined, display: 'flex', justifyContent: 'center' }}
          />
        ) : (
          <Box
            component="img"
            src={displaySrc}
            alt=""
            decoding="async"
            sx={{ display: 'block', width: '100%', maxWidth: fullWidth ? 'none' : 900, mx: 'auto', userSelect: 'none', WebkitUserSelect: 'none' }}
          />
        )
      ) : hasBox ? (
        <Box
          sx={{
            width: '100%',
            aspectRatio: `${natW} / ${natH}`,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          {phase === 'error' ? (
            <Box
              onClick={retry}
              sx={{ py: 4, textAlign: 'center', color: 'text.secondary', cursor: 'pointer', '&:hover': { color: 'primary.main' } }}
            >
              <BrokenImageIcon sx={{ fontSize: 42, mb: 1 }} />
              <Typography variant="body2">图片加载失败，点击重试</Typography>
            </Box>
          ) : phase === 'loading' ? (
            <CircularProgress size={28} thickness={4} />
          ) : null}
        </Box>
      ) : phase === 'error' ? (
        <Box
          onClick={retry}
          sx={{
            py: 8,
            textAlign: 'center',
            color: 'text.secondary',
            cursor: 'pointer',
            '&:hover': { color: 'primary.main' },
          }}
        >
          <BrokenImageIcon sx={{ fontSize: 42, mb: 1 }} />
          <Typography variant="body2">图片加载失败，点击重试</Typography>
        </Box>
      ) : (
        <Box sx={{ py: 8, textAlign: 'center' }}>
          <CircularProgress size={28} thickness={4} />
          {needDescramble && (
            <Typography variant="caption" color="text.secondary" display="block" mt={1}>
              解码中…
            </Typography>
          )}
        </Box>
      )}
    </Box>
  )
}

// 属性均为原始值，浅比较即可稳定跳过未变化页面的重渲染
export default memo(DscImage)
