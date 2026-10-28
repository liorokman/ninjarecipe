package images

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liorokman/ninjarecipe/internal/domain"
)

func jpegImg(w, h int) []byte {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, color.RGBA{uint8(x), uint8(y), 7, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, m, nil)
	return b.Bytes()
}

func TestProcessResizesAndReencodes(t *testing.T) {
	p := NewProcessor(1, 0, 0)
	out, err := p.Process(context.Background(), bytes.NewReader(jpegImg(3200, 2400)))
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != 1600 || out.Height != 1200 {
		t.Fatalf("full %dx%d", out.Width, out.Height)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out.Thumb))
	if err != nil || cfg.Width != 480 || cfg.Height != 360 {
		t.Fatalf("thumb %+v %v", cfg, err)
	}
	// never upscale
	small, _ := p.Process(context.Background(), bytes.NewReader(jpegImg(100, 50)))
	if small.Width != 100 || small.Height != 50 {
		t.Fatal("upscaled")
	}
}

func TestProcessFormats(t *testing.T) {
	p := NewProcessor(1, 0, 0)
	var pb bytes.Buffer
	m := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	m.Set(1, 1, color.NRGBA{255, 0, 0, 0}) // transparent pixel must flatten to white
	_ = png.Encode(&pb, m)
	out, err := p.Process(context.Background(), &pb)
	if err != nil {
		t.Fatalf("png: %v", err)
	}
	img, _ := jpeg.Decode(bytes.NewReader(out.Full))
	r, g, b, _ := img.At(1, 1).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Fatalf("alpha not flattened onto white: %d %d %d", r>>8, g>>8, b>>8)
	}
	var gb bytes.Buffer
	_ = gif.Encode(&gb, image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White}), nil)
	if _, err := p.Process(context.Background(), &gb); !errors.Is(err, domain.ErrUnsupportedMedia) {
		t.Fatalf("gif must be 415: %v", err)
	}
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)
	heic = append(heic, make([]byte, 20)...)
	if _, err := p.Process(context.Background(), bytes.NewReader(heic)); !errors.Is(err, ErrHEIC) || !errors.Is(err, domain.ErrUnsupportedMedia) {
		t.Fatalf("heic: %v", err)
	}
	for _, junk := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), []byte("RIFF\x00\x00\x00\x00WEBPjunk"), {}, []byte("hello")} {
		if _, err := p.Process(context.Background(), bytes.NewReader(junk)); !errors.Is(err, domain.ErrUnsupportedMedia) {
			t.Fatalf("junk %q: %v", junk, err)
		}
	}
}

func TestProcessLimits(t *testing.T) {
	p := NewProcessor(1, 1000, 0)
	if _, err := p.Process(context.Background(), bytes.NewReader(jpegImg(100, 100))); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("pixel cap: %v", err)
	}
	p = NewProcessor(1, 0, 500)
	if _, err := p.Process(context.Background(), bytes.NewReader(jpegImg(200, 200))); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("byte cap: %v", err)
	}
	// worker semaphore: a second request waits and then gets ErrBusy
	p = NewProcessor(1, 0, 0)
	p.BusyWait = 30 * time.Millisecond
	p.sem <- struct{}{}
	if _, err := p.Process(context.Background(), bytes.NewReader(jpegImg(10, 10))); !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
}

func TestOrientation(t *testing.T) {
	// 2x1 image: left red, right blue. Orientation 6 (rotate 90 CW) => 1x2, red on top.
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	src.Set(1, 0, color.RGBA{0, 0, 255, 255})
	o := orient(src, 6)
	if o.Bounds().Dx() != 1 || o.Bounds().Dy() != 2 {
		t.Fatalf("bounds %v", o.Bounds())
	}
	if r, _, _, _ := o.At(0, 0).RGBA(); r == 0 {
		t.Fatal("red should be on top after 90 CW")
	}
	if orient(src, 1) != image.Image(src) || orient(src, 99) != image.Image(src) {
		t.Fatal("identity orientations")
	}
	if b := orient(src, 3).Bounds(); b.Dx() != 2 {
		t.Fatal("180 keeps size")
	}
}

func TestStorageLayoutAndSweep(t *testing.T) {
	dir := t.TempDir()
	key, err := Store(dir, &Processed{Full: []byte("f"), Thumb: []byte("t")})
	if err != nil || !ValidKey(key) {
		t.Fatalf("%v %q", err, key)
	}
	want := filepath.Join(dir, "media", key[:2], key, "full.jpg")
	if VariantPath(dir, key, "full") != want {
		t.Fatal("layout")
	}
	if b, _ := os.ReadFile(want); string(b) != "f" {
		t.Fatal("content")
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	if len(ents) != 0 {
		t.Fatal("tmp must be empty after rename")
	}
	k2, err := Copy(dir, key)
	if err != nil || k2 == key {
		t.Fatalf("copy: %v", err)
	}
	if _, err := os.Stat(VariantPath(dir, k2, "thumb")); err != nil {
		t.Fatal(err)
	}
	Remove(dir, key)
	Remove(dir, "../../etc") // invalid key is ignored
	if _, err := os.Stat(Dir(dir, key)); err == nil {
		t.Fatal("not removed")
	}
	// sweep: keeps referenced and fresh, removes old orphans and stale tmp
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(Dir(dir, k2), old, old)
	_ = os.MkdirAll(filepath.Join(dir, "tmp", "stale"), 0o755)
	_ = os.Chtimes(filepath.Join(dir, "tmp", "stale"), old, old)
	if n := Sweep(context.Background(), dir, map[string]bool{k2: true}, time.Hour, time.Now()); n != 1 {
		t.Fatalf("sweep with reference: %d (only stale tmp)", n)
	}
	if n := Sweep(context.Background(), dir, nil, time.Hour, time.Now()); n != 1 {
		t.Fatalf("sweep orphan: %d", n)
	}
	for _, bad := range []string{"", "short", "ZZZZ" + key[4:], key + "0"} {
		if ValidKey(bad) {
			t.Errorf("%q valid", bad)
		}
	}
	EmptyTmp(dir)
	if len(Placeholder()) < 100 {
		t.Fatal("placeholder")
	}
}

func TestPublicAddrClassification(t *testing.T) {
	bad := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "100.127.255.255", "0.0.0.0", "224.0.0.1", "::1", "fe80::1",
		"fc00::1", "fd12::1", "::", "ff02::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b:1::1", "2002:7f00:1::1", "255.255.255.255"}
	for _, s := range bad {
		if IsPublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must be rejected", s)
		}
	}
	good := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111", "::ffff:8.8.8.8", "64:ff9b::808:808", "100.128.0.1"}
	for _, s := range good {
		if !IsPublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
}

func TestFetcher(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write(jpegImg(10, 10))
		case "/404":
			http.NotFound(w, r)
		case "/big":
			_, _ = w.Write(make([]byte, 5000))
		case "/redir":
			http.Redirect(w, r, "/redir2", 302)
		case "/redir2":
			http.Redirect(w, r, "/redir3", 302)
		case "/redir3":
			http.Redirect(w, r, "/redir4", 302)
		case "/redir4":
			http.Redirect(w, r, "/redir5", 302)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	// real guard: loopback is blocked even with http allowed
	f := &Fetcher{AllowHTTP: true, MaxBytes: 1000}
	if _, err := f.Fetch(ctx, srv.URL+"/ok"); err == nil || err.(*FetchError).Code != "blocked" {
		t.Fatalf("loopback must be blocked: %v", err)
	}
	if hits != 0 {
		t.Fatal("connection must be refused before any request")
	}
	if _, err := (&Fetcher{}).Fetch(ctx, srv.URL+"/ok"); err.(*FetchError).Code != "blocked" {
		t.Fatalf("http needs opt-in: %v", err)
	}
	if _, err := f.Fetch(ctx, "ftp://x/y"); err.(*FetchError).Code != "blocked" {
		t.Fatal("scheme")
	}
	if _, err := f.Fetch(ctx, "::bad::"); err.(*FetchError).Code != "invalid_url" {
		t.Fatal("invalid url")
	}
	// with the guard off (tests only) the rest of the behaviour
	f = &Fetcher{AllowHTTP: true, AllowAll: true, MaxBytes: 1000}
	if b, err := f.Fetch(ctx, srv.URL+"/ok"); err != nil || len(b) < 100 {
		t.Fatalf("ok: %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/404"); err.(*FetchError).Status != 404 || err.(*FetchError).Code != "fetch_failed" {
		t.Fatalf("404: %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/big"); err.(*FetchError).Code != "too_large" {
		t.Fatalf("size cap: %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/redir"); err == nil {
		t.Fatal("more than 3 redirects must fail")
	}
}
