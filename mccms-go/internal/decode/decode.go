// Package decode 负责还原站点的图片乱序。
//
// 背景
// ----
// 部分站点（如香香腐宅的新章节）会把整页图切成 N 条**竖向条带**并倒序后下发，
// 由前端用 canvas 还原。站点前端 `do_mergeImg` 的等价逻辑是：
//
//	for i := 1; i <= N; i++ {
//	    if H >= 4000 {                       // 超高图：原样拷贝（等于不解码）
//	        draw(src[i], dst[i])
//	    } else if i == N {                   // 最左那条（含宽度余数）落到最右
//	        w := W - (W/N)*(N-1)
//	        draw(src[0:w], dst[(W/N)*(N-1):])
//	    } else {                             // 右边第 i 条 -> 左边第 i 个位置
//	        w := W / N
//	        draw(src[W-(W/N)*i : W-(W/N)*i+w], dst[(W/N)*(i-1):])
//	    }
//	}
//
// 即：把 N 条竖带整体倒序。倒序是自逆运算，所以同一段逻辑既能乱序也能还原。
package decode

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif" // 支持 gif 解码

	"golang.org/x/image/webp"
)

// VeryTallHeight 站点前端对 height >= 4000 的图做原样拷贝，本库保持一致。
const VeryTallHeight = 4000

// NeedsDecode 判断是否需要还原。
func NeedsDecode(width, height, n int) bool {
	if n <= 1 {
		return false
	}
	if width/n < 1 {
		return false
	}
	// 与站点前端保持一致：超高图不解码，避免把本来就正常的超长条漫弄坏
	return height < VeryTallHeight
}

// ReverseVerticalStrips 把图像按 n 条竖带倒序，返回新图像。
func ReverseVerticalStrips(src image.Image, n int) (image.Image, error) {
	if n <= 1 {
		return src, nil
	}

	b := src.Bounds()
	width, height := b.Dx(), b.Dy()
	strip := width / n
	if strip < 1 {
		return nil, fmt.Errorf("图片宽度 %d 不足以切成 %d 条竖带", width, n)
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	for i := 1; i <= n; i++ {
		var sw, sx, dx int
		if i == n {
			sw = width - strip*(n-1)
			sx, dx = 0, strip*(n-1)
		} else {
			sw = strip
			sx, dx = width-strip*i, strip*(i-1)
		}
		paste(dst, src, b.Min.X+sx, b.Min.Y, dx, 0, sw, height)
	}

	return dst, nil
}

// paste 把 src 中 (sx,sy) 起 sw×sh 的区域贴到 dst 的 (dx,dy)。
func paste(dst *image.RGBA, src image.Image, sx, sy, dx, dy, sw, sh int) {
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			dst.Set(dx+x, dy+y, src.At(sx+x, sy+y))
		}
	}
}

// DecodeImageBytes 在内存里还原图片字节。
//
// 返回值：还原后的字节与 MIME 后缀（如 ".webp"）。
// 若无需还原则原样返回输入。
func DecodeImageBytes(data []byte, n int) ([]byte, string, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, "", err
	}

	b := img.Bounds()
	if !NeedsDecode(b.Dx(), b.Dy(), n) {
		return data, mimeSuffix(format), nil
	}

	out, err := ReverseVerticalStrips(img, n)
	if err != nil {
		return data, "", err
	}

	var buf bytes.Buffer
	// 竖带宽度通常不是压缩块大小的整数倍，无法在压缩域无损搬移，
	// 因此必须重新编码；统一用高质量的 PNG/JPEG/WebP 输出。
	switch strings.ToLower(format) {
	case "jpeg":
		if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 95}); err != nil {
			return data, "", err
		}
		return buf.Bytes(), ".jpg", nil
	case "png":
		if err := png.Encode(&buf, out); err != nil {
			return data, "", err
		}
		return buf.Bytes(), ".png", nil
	default:
		// webp / gif 等统一转 PNG，避免引入额外编码器
		if err := png.Encode(&buf, out); err != nil {
			return data, "", err
		}
		return buf.Bytes(), ".png", nil
	}
}

// DecodeFile 就地还原一个图片文件（保持原后缀对应的格式）。
func DecodeFile(path string, n int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	b := img.Bounds()
	if !NeedsDecode(b.Dx(), b.Dy(), n) {
		return nil
	}

	out, err := ReverseVerticalStrips(img, n)
	if err != nil {
		return err
	}

	suffix := strings.ToLower(filepath.Ext(path))
	var buf bytes.Buffer
	switch suffix {
	case ".jpg", ".jpeg":
		if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 95}); err != nil {
			return err
		}
	case ".png":
		if err := png.Encode(&buf, out); err != nil {
			return err
		}
	default:
		// 后缀声称是 webp，但站点实际下发的常是 JPEG（实测如此），
		// 这里按后缀输出 PNG 以保证无损且一定能写入。
		if strings.EqualFold(format, "png") {
			if err := png.Encode(&buf, out); err != nil {
				return err
			}
		} else {
			if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 95}); err != nil {
				return err
			}
		}
	}

	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func mimeSuffix(format string) string {
	switch strings.ToLower(format) {
	case "jpeg":
		return ".jpg"
	case "png":
		return ".png"
	case "gif":
		return ".gif"
	case "webp":
		return ".webp"
	}
	return ""
}

// 确保 webp 解码器被链接进来
var _ = webp.Decode
