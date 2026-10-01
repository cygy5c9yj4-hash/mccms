package decode

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makeStriped 生成 n 条颜色各异的竖带图，用于精确断言顺序。
func makeStriped(width, height, n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	palette := []color.RGBA{
		{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255},
		{0, 255, 255, 255}, {255, 0, 255, 255}, {128, 128, 128, 255}, {255, 128, 0, 255},
		{0, 128, 255, 255}, {128, 255, 0, 255}, {255, 0, 128, 255}, {0, 0, 0, 255},
	}
	strip := width / n
	for i := 0; i < n; i++ {
		x0 := strip * i
		x1 := strip * (i + 1)
		if i == n-1 {
			x1 = width
		}
		c := palette[i%len(palette)]
		for x := x0; x < x1; x++ {
			for y := 0; y < height; y++ {
				img.Set(x, y, c)
			}
		}
	}
	return img
}

func stripColors(img image.Image, n int) []color.Color {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	strip := width / n
	out := make([]color.Color, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, img.At(b.Min.X+strip*i, b.Min.Y+height/2))
	}
	return out
}

func TestReverseStrips(t *testing.T) {
	n := 8
	src := makeStriped(240, 40, n)
	before := stripColors(src, n)

	out, err := ReverseVerticalStrips(src, n)
	if err != nil {
		t.Fatalf("ReverseVerticalStrips: %v", err)
	}
	after := stripColors(out, n)

	for i := range before {
		if after[i] != before[len(before)-1-i] {
			t.Fatalf("第 %d 条未倒序: got %v want %v", i, after[i], before[len(before)-1-i])
		}
	}
}

// 倒序是自逆运算——这正是「站点用它乱序、我们用它还原」成立的前提。
func TestReverseIsInvolution(t *testing.T) {
	n := 11
	src := makeStriped(649, 60, n)

	once, err := ReverseVerticalStrips(src, n)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := ReverseVerticalStrips(once, n)
	if err != nil {
		t.Fatal(err)
	}

	b := src.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if src.At(x, y) != twice.At(x, y) {
				t.Fatalf("二次倒序未回到原图，首个不同像素 (%d,%d)", x, y)
			}
		}
	}
}

// 宽度不能整除 n 时，最左那条（含余数）要落到最右。
func TestRemainderStripMapping(t *testing.T) {
	const (
		width = 100
		n     = 3
		strip = width / n // 33
	)

	img := image.NewRGBA(image.Rect(0, 0, width, 4))
	for x := 0; x < width; x++ {
		img.Set(x, 0, color.RGBA{uint8(x), 0, 0, 255})
	}

	out, err := ReverseVerticalStrips(img, n)
	if err != nil {
		t.Fatal(err)
	}

	// 期望映射（严格复刻站点 do_mergeImg）：
	//   i=1: src[67:100] -> dst[0:33]
	//   i=2: src[34:67]  -> dst[33:66]
	//   i=3: src[0:34]   -> dst[66:100]
	want := make([]uint8, width)
	for i := 1; i <= n; i++ {
		var sw, sx, dx int
		if i == n {
			sw, sx, dx = width-strip*(n-1), 0, strip*(n-1)
		} else {
			sw, sx, dx = strip, width-strip*i, strip*(i-1)
		}
		for k := 0; k < sw; k++ {
			want[dx+k] = uint8((sx + k) % 256)
		}
	}

	for x := 0; x < width; x++ {
		got := out.At(x, 0).(color.RGBA).R
		if got != want[x] {
			t.Fatalf("x=%d got=%d want=%d", x, got, want[x])
		}
	}
}

func TestNeedsDecode(t *testing.T) {
	cases := []struct {
		w, h, n int
		want    bool
	}{
		{649, 993, 11, true},
		{649, 993, 1, false},
		{649, 993, 0, false},
		{800, 4000, 11, false}, // 站点前端对 >=4000 的图原样拷贝
		{800, 9000, 11, false},
		{800, 3999, 11, true},
		{4, 100, 10, false}, // 宽度不足以切分
	}
	for _, c := range cases {
		if got := NeedsDecode(c.w, c.h, c.n); got != c.want {
			t.Errorf("NeedsDecode(%d,%d,%d) = %v, want %v", c.w, c.h, c.n, got, c.want)
		}
	}
}

func TestDecodeFile(t *testing.T) {
	n := 11
	original := makeStriped(649, 80, n)
	scrambled, err := ReverseVerticalStrips(original, n)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "s.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, scrambled); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := DecodeFile(path, n); err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}

	b := original.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if original.At(x, y) != img.At(x, y) {
				t.Fatalf("还原结果与原图不同，首个差异像素 (%d,%d)", x, y)
			}
		}
	}
}

func TestDecodeBytesSkipsWhenNotNeeded(t *testing.T) {
	original := makeStriped(300, 60, 3)
	var buf bytes.Buffer
	if err := png.Encode(&buf, original); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()

	out, _, err := DecodeImageBytes(raw, 1) // n=1 -> 不需要还原
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("不需要还原时应当原样返回输入字节")
	}
}
