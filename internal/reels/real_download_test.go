package reels

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/KappaShilaff/reelsovoz/internal/config"
)

func TestRealInstagramDownloadToDisk(t *testing.T) {
	if os.Getenv("REELSOVOZ_REAL_DOWNLOAD") != "1" {
		t.Skip("set REELSOVOZ_REAL_DOWNLOAD=1 to run real download smoke test")
	}

	rawURL := os.Getenv("REELSOVOZ_REAL_DOWNLOAD_URL")
	if rawURL == "" {
		t.Fatal("REELSOVOZ_REAL_DOWNLOAD_URL is required")
	}

	outputPath := os.Getenv("REELSOVOZ_REAL_DOWNLOAD_OUTPUT")
	if outputPath == "" {
		outputPath = filepath.Join("test-output", "reel.mp4")
	}
	outputPath = repoPath(t, outputPath)
	expectVideo := os.Getenv("REELSOVOZ_REAL_DOWNLOAD_EXPECT_VIDEO") == "1"
	instagramCookiesFile, err := config.InstagramCookiesFileFromEnv()
	if err != nil {
		t.Fatalf("load Instagram cookies: %v", err)
	}

	downloaded, err := Downloader{
		YTDLPPath:            envOr("YT_DLP_PATH", "yt-dlp"),
		FFmpegPath:           envOr("FFMPEG_PATH", "ffmpeg"),
		InstagramCookiesFile: instagramCookiesFile,
		Timeout:              envDurationOr("DOWNLOAD_TIMEOUT", 120*time.Second),
		MaxBytes:             envInt64Or("MAX_VIDEO_BYTES", 50_331_648),
	}.Download(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if len(downloaded) == 0 {
		t.Fatal("downloaded media list is empty")
	}
	first := downloaded[0]
	if len(first.Bytes) == 0 {
		t.Fatal("downloaded media is empty")
	}
	if expectVideo && first.Kind != MediaKindVideo {
		t.Fatalf("downloaded media kind = %q, want video; Instagram audio metadata may require INSTAGRAM_COOKIES_FILE", first.Kind)
	}
	if first.Kind == MediaKindVideo && !looksLikeMediaContainer(first.Bytes) {
		t.Fatalf("downloaded bytes do not look like mp4/webm media; first bytes=% x", first.Bytes[:min(len(first.Bytes), 16)])
	}
	if first.Kind == MediaKindPhoto && !looksLikeJPEG(first.Bytes) {
		t.Fatalf("downloaded bytes do not look like jpeg; first bytes=% x", first.Bytes[:min(len(first.Bytes), 16)])
	}
	if first.Kind == MediaKindPhoto && filepath.Ext(outputPath) == ".mp4" {
		outputPath = outputPath[:len(outputPath)-len(filepath.Ext(outputPath))] + ".jpg"
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	if err := os.WriteFile(outputPath, first.Bytes, 0o644); err != nil {
		t.Fatalf("write downloaded media: %v", err)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat downloaded video: %v", err)
	}
	if info.Size() <= 0 {
		t.Fatalf("downloaded file size = %d", info.Size())
	}

	t.Logf("wrote %s (%d bytes)", outputPath, info.Size())
}

func looksLikeMediaContainer(body []byte) bool {
	if len(body) < 12 {
		return false
	}
	return bytes.Contains(body[:min(len(body), 32)], []byte("ftyp")) ||
		bytes.HasPrefix(body, []byte{0x1a, 0x45, 0xdf, 0xa3})
}

func looksLikeJPEG(body []byte) bool {
	return len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff
}

func envOr(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64Or(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	var parsed int64
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func repoPath(t *testing.T, path string) string {
	t.Helper()
	if filepath.IsAbs(path) {
		return path
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test filename")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", path)
}
