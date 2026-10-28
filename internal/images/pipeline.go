// Package images implements the image pipeline of spec 20 §9: sniff, orient, resize, re-encode, store.
package images

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // register decoder
	"io"
	"net/http"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/liorokman/ninjarecipe/internal/domain"
)

// Processed holds the two JPEG variants.
type Processed struct {
	Full, Thumb   []byte
	Width, Height int // of the full variant
}

// Bytes is the total size on disk.
func (p *Processed) Bytes() int64 { return int64(len(p.Full) + len(p.Thumb)) }

// ErrHEIC marks HEIC/HEIF input (wraps ErrUnsupportedMedia).
var ErrHEIC = fmt.Errorf("%w: heic", domain.ErrUnsupportedMedia)

// Processor runs Process under a global semaphore (IMAGE_WORKERS).
type Processor struct {
	sem       chan struct{}
	MaxPixels int64
	MaxBytes  int64
	BusyWait  time.Duration
}

// NewProcessor returns a processor with the given concurrency and limits.
func NewProcessor(workers int, maxPixels, maxBytes int64) *Processor {
	if workers < 1 {
		workers = 1
	}
	if maxPixels <= 0 {
		maxPixels = 50_000_000
	}
	if maxBytes <= 0 {
		maxBytes = 15 << 20
	}
	return &Processor{sem: make(chan struct{}, workers), MaxPixels: maxPixels, MaxBytes: maxBytes, BusyWait: 60 * time.Second}
}

// Process reads r (capped at MaxBytes), validates and re-encodes. Errors wrap domain.ErrTooLarge,
// domain.ErrUnsupportedMedia or domain.ErrBusy.
func (p *Processor) Process(ctx context.Context, r io.Reader) (*Processed, error) {
	data, err := io.ReadAll(io.LimitReader(r, p.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > p.MaxBytes {
		return nil, domain.ErrTooLarge
	}
	wait := time.NewTimer(p.BusyWait)
	defer wait.Stop()
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-wait.C:
		return nil, domain.ErrBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return process(data, p.MaxPixels)
}

func isHEIC(d []byte) bool {
	if len(d) >= 12 && string(d[4:8]) == "ftyp" {
		switch string(d[8:12]) {
		case "heic", "heix", "hevc", "hevx", "mif1", "msf1":
			return true
		}
	}
	return false
}

func process(data []byte, maxPixels int64) (*Processed, error) {
	if isHEIC(data) {
		return nil, ErrHEIC
	}
	ct := http.DetectContentType(data)
	if ct == "application/octet-stream" && len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		ct = "image/webp"
	}
	var cfg image.Config
	var err error
	switch ct {
	case "image/jpeg", "image/png":
		cfg, _, err = image.DecodeConfig(bytes.NewReader(data))
	case "image/webp":
		cfg, err = webp.DecodeConfig(bytes.NewReader(data))
	default:
		return nil, domain.ErrUnsupportedMedia
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrUnsupportedMedia, err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, domain.ErrTooLarge
	}
	orientation := 1
	if ct == "image/jpeg" {
		if x, err := exif.Decode(bytes.NewReader(data)); err == nil {
			if t, err := x.Get(exif.Orientation); err == nil {
				if o, err := t.Int(0); err == nil {
					orientation = o
				}
			}
		}
	}
	var src image.Image
	switch ct {
	case "image/webp":
		src, err = webp.Decode(bytes.NewReader(data))
	default:
		src, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrUnsupportedMedia, err)
	}
	src = orient(src, orientation)
	src = flatten(src)
	full := resize(src, 1600)
	var fb, tb bytes.Buffer
	if err := jpeg.Encode(&fb, full, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	if err := jpeg.Encode(&tb, resize(src, 480), &jpeg.Options{Quality: 78}); err != nil {
		return nil, err
	}
	b := full.Bounds()
	return &Processed{Full: fb.Bytes(), Thumb: tb.Bytes(), Width: b.Dx(), Height: b.Dy()}, nil
}

func resize(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return src
	}
	var nw, nh int
	if w >= h {
		nw, nh = maxEdge, h*maxEdge/w
	} else {
		nw, nh = w*maxEdge/h, maxEdge
	}
	nw, nh = max(nw, 1), max(nh, 1)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

func flatten(src image.Image) image.Image {
	b := src.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(out, b, src, b.Min, draw.Over)
	return out
}

// orient applies an EXIF orientation (1-8).
func orient(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	swap := o >= 5
	var dst *image.RGBA
	if swap {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// ErrNotProcessable is returned for decode failures that are not covered by the domain errors.
var ErrNotProcessable = errors.New("image not processable")
