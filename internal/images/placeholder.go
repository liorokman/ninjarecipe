package images

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
)

var (
	phOnce sync.Once
	phData []byte
)

// Placeholder returns a small neutral JPEG (4:3) served when a media file is missing.
func Placeholder() []byte {
	phOnce.Do(func() {
		img := image.NewRGBA(image.Rect(0, 0, 320, 240))
		for y := 0; y < 240; y++ {
			for x := 0; x < 320; x++ {
				v := uint8(214 + (x+y)/40)
				img.Set(x, y, color.RGBA{v, v - 6, v - 14, 255})
			}
		}
		var b bytes.Buffer
		_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 70})
		phData = b.Bytes()
	})
	return phData
}
