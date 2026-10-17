package receipt

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"

	"simpleAI/internal/receipt/filecheck"
)

// shrinkSide — длинная сторона картинки для классификатора. Чтобы отличить
// чек от другого фото, мелкий текст читать не нужно, а у бесплатных
// провайдеров каждый пиксель стоит входных токенов.
const shrinkSide = 512

// maxDecodePixels — картинку больше этого не декодируем: маленький, хорошо
// сжатый PNG может заявить 30000×30000 и потребовать гигабайты памяти.
// Такой файл уходит классификатору как есть.
const maxDecodePixels = 50_000_000

// maxSamples — сколько точек по каждой оси блока усредняется. Полный обход
// 12-мегапиксельного фото стоит секунды CPU на каждый снимок, а для
// превью классификатора хватает выборки.
const maxSamples = 4

// shrink уменьшает картинку до shrinkSide по длинной стороне и пережимает в
// JPEG. WEBP и PDF стандартная библиотека не декодирует — они, как и уже
// маленькие картинки, уходят без изменений.
func shrink(f filecheck.File) filecheck.File {
	var decode func([]byte) (image.Image, error)
	var config func([]byte) (image.Config, error)
	switch f.Format {
	case filecheck.JPEG:
		decode = func(b []byte) (image.Image, error) { return jpeg.Decode(bytes.NewReader(b)) }
		config = func(b []byte) (image.Config, error) { return jpeg.DecodeConfig(bytes.NewReader(b)) }
	case filecheck.PNG:
		decode = func(b []byte) (image.Image, error) { return png.Decode(bytes.NewReader(b)) }
		config = func(b []byte) (image.Config, error) { return png.DecodeConfig(bytes.NewReader(b)) }
	case filecheck.GIF:
		decode = func(b []byte) (image.Image, error) { return gif.Decode(bytes.NewReader(b)) }
		config = func(b []byte) (image.Config, error) { return gif.DecodeConfig(bytes.NewReader(b)) }
	default:
		return f
	}
	cfg, err := config(f.Data)
	if err != nil || int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return f
	}
	src, err := decode(f.Data)
	if err != nil {
		return f
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= shrinkSide && h <= shrinkSide {
		return f
	}
	nw, nh := shrinkSide, h*shrinkSide/w
	if h > w {
		nw, nh = w*shrinkSide/h, shrinkSide
	}
	nw, nh = max(nw, 1), max(nh, 1)

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := range nh {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+max((y+1)*h/nh, y*h/nh+1)
		for x := range nw {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+max((x+1)*w/nw, x*w/nw+1)
			dst.Set(x, y, average(src, x0, y0, x1, y1))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 80}); err != nil {
		return f
	}
	return filecheck.File{Format: filecheck.JPEG, Data: buf.Bytes()}
}

// average — средний цвет прямоугольника [x0,x1)×[y0,y1) по сетке не больше
// maxSamples×maxSamples точек: усреднение, а не одна точка, чтобы тонкие
// линии текста не пропадали.
func average(src image.Image, x0, y0, x1, y1 int) color.Color {
	sy, sx := max((y1-y0)/maxSamples, 1), max((x1-x0)/maxSamples, 1)
	var r, g, b, a, n uint64
	for y := y0; y < y1; y += sy {
		for x := x0; x < x1; x += sx {
			cr, cg, cb, ca := src.At(x, y).RGBA()
			r, g, b, a, n = r+uint64(cr), g+uint64(cg), b+uint64(cb), a+uint64(ca), n+1
		}
	}
	return color.RGBA64{R: uint16(r / n), G: uint16(g / n), B: uint16(b / n), A: uint16(a / n)} //nolint:gosec // G115: среднее ≤ 0xFFFF
}
