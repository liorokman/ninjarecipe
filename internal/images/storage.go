package images

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// NewKey returns a random 32-hex storage key.
func NewKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ValidKey reports whether k looks like a storage key.
func ValidKey(k string) bool {
	if len(k) != 32 {
		return false
	}
	for _, c := range k {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Dir is media/{key[0:2]}/{key}.
func Dir(dataDir, key string) string { return filepath.Join(dataDir, "media", key[:2], key) }

// VariantPath is media/{key[0:2]}/{key}/{full|thumb}.jpg.
func VariantPath(dataDir, key, variant string) string {
	return filepath.Join(Dir(dataDir, key), variant+".jpg")
}

func writeSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Store writes both variants into tmp/, fsyncs and renames the directory into place. Returns the key.
func Store(dataDir string, p *Processed) (string, error) {
	key := NewKey()
	tmpRoot := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(tmpRoot, "up-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := writeSync(filepath.Join(tmp, "full.jpg"), p.Full); err != nil {
		return "", err
	}
	if err := writeSync(filepath.Join(tmp, "thumb.jpg"), p.Thumb); err != nil {
		return "", err
	}
	dst := Dir(dataDir, key)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", fmt.Errorf("store media: %w", err)
	}
	return key, nil
}

// Copy duplicates the files of a key under a new key (recipe copy).
func Copy(dataDir, srcKey string) (string, error) {
	p := &Processed{}
	for _, v := range []string{"full", "thumb"} {
		f, err := os.Open(VariantPath(dataDir, srcKey, v))
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return "", err
		}
		if v == "full" {
			p.Full = b
		} else {
			p.Thumb = b
		}
	}
	return Store(dataDir, p)
}

// Remove deletes the files of a key; failures are logged, never returned to callers' flows.
func Remove(dataDir, key string) {
	if !ValidKey(key) {
		return
	}
	if err := os.RemoveAll(Dir(dataDir, key)); err != nil {
		slog.Warn("media removal failed", "key", key, "error", err)
	}
}

// Sweep deletes media directories without a DB row older than olderThan, and stale tmp entries.
// active is the set of keys that have a row. Returns the number of removed entries.
func Sweep(ctx context.Context, dataDir string, active map[string]bool, olderThan time.Duration, now time.Time) int {
	n := 0
	base := filepath.Join(dataDir, "media")
	prefixes, _ := os.ReadDir(base)
	for _, p := range prefixes {
		if !p.IsDir() {
			continue
		}
		keys, _ := os.ReadDir(filepath.Join(base, p.Name()))
		for _, k := range keys {
			if ctx.Err() != nil {
				return n
			}
			if active[k.Name()] || !k.IsDir() {
				continue
			}
			if info, err := k.Info(); err == nil && now.Sub(info.ModTime()) > olderThan {
				if os.RemoveAll(filepath.Join(base, p.Name(), k.Name())) == nil {
					n++
				}
			}
		}
		_ = os.Remove(filepath.Join(base, p.Name())) // only succeeds if empty
	}
	tmp := filepath.Join(dataDir, "tmp")
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > olderThan {
			if os.RemoveAll(filepath.Join(tmp, e.Name())) == nil {
				n++
			}
		}
	}
	return n
}

// EmptyTmp clears the tmp dir (startup).
func EmptyTmp(dataDir string) {
	tmp := filepath.Join(dataDir, "tmp")
	ents, _ := os.ReadDir(tmp)
	for _, e := range ents {
		_ = os.RemoveAll(filepath.Join(tmp, e.Name()))
	}
	_ = os.MkdirAll(tmp, 0o755)
}
