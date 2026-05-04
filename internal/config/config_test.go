package config

import (
	"encoding/base64"
	"errors"
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.TelegramBotToken != "token" {
		t.Fatalf("TelegramBotToken = %q", cfg.TelegramBotToken)
	}
	if cfg.TelegramStorageChatID != 0 {
		t.Fatalf("TelegramStorageChatID = %d", cfg.TelegramStorageChatID)
	}
	if cfg.UserStorageFile != "/data/reelsovoz-users.json" {
		t.Fatalf("UserStorageFile = %q", cfg.UserStorageFile)
	}
	if cfg.YTDLPPath != "yt-dlp" {
		t.Fatalf("YTDLPPath = %q", cfg.YTDLPPath)
	}
	if cfg.FFmpegPath != "ffmpeg" {
		t.Fatalf("FFmpegPath = %q", cfg.FFmpegPath)
	}
	if cfg.InstagramCookiesFile != "" {
		t.Fatalf("InstagramCookiesFile = %q", cfg.InstagramCookiesFile)
	}
	if cfg.DownloadTimeout != 90*time.Second {
		t.Fatalf("DownloadTimeout = %s", cfg.DownloadTimeout)
	}
	if cfg.PrepareTimeout != 10*time.Minute {
		t.Fatalf("PrepareTimeout = %s", cfg.PrepareTimeout)
	}
	if cfg.TelegramUploadRetries != 3 {
		t.Fatalf("TelegramUploadRetries = %d", cfg.TelegramUploadRetries)
	}
	if cfg.TelegramUploadTimeout != 120*time.Second {
		t.Fatalf("TelegramUploadTimeout = %s", cfg.TelegramUploadTimeout)
	}
	if cfg.MaxVideoBytes != 50331648 {
		t.Fatalf("MaxVideoBytes = %d", cfg.MaxVideoBytes)
	}
	if cfg.HealthAddr != ":8000" {
		t.Fatalf("HealthAddr = %q", cfg.HealthAddr)
	}
	if cfg.MetricsAddr != ":10000" {
		t.Fatalf("MetricsAddr = %q", cfg.MetricsAddr)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_STORAGE_CHAT_ID", "42")
	t.Setenv("YT_DLP_PATH", "/usr/local/bin/yt-dlp")
	t.Setenv("FFMPEG_PATH", "/usr/local/bin/ffmpeg")
	t.Setenv("INSTAGRAM_COOKIES_FILE", "/run/secrets/ig-cookies.txt")
	t.Setenv("DOWNLOAD_TIMEOUT", "2m30s")
	t.Setenv("PREPARE_TIMEOUT", "12m")
	t.Setenv("TELEGRAM_UPLOAD_RETRIES", "5")
	t.Setenv("TELEGRAM_UPLOAD_TIMEOUT", "3m")
	t.Setenv("MAX_VIDEO_BYTES", "123")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:9000")
	t.Setenv("METRICS_ADDR", "127.0.0.1:10000")
	t.Setenv("USER_STORAGE_FILE", "/tmp/reelsovoz-users.json")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.TelegramStorageChatID != 42 {
		t.Fatalf("TelegramStorageChatID = %d", cfg.TelegramStorageChatID)
	}
	if cfg.UserStorageFile != "/tmp/reelsovoz-users.json" {
		t.Fatalf("UserStorageFile = %q", cfg.UserStorageFile)
	}
	if cfg.YTDLPPath != "/usr/local/bin/yt-dlp" {
		t.Fatalf("YTDLPPath = %q", cfg.YTDLPPath)
	}
	if cfg.FFmpegPath != "/usr/local/bin/ffmpeg" {
		t.Fatalf("FFmpegPath = %q", cfg.FFmpegPath)
	}
	if cfg.InstagramCookiesFile != "/run/secrets/ig-cookies.txt" {
		t.Fatalf("InstagramCookiesFile = %q", cfg.InstagramCookiesFile)
	}
	if cfg.DownloadTimeout != 150*time.Second {
		t.Fatalf("DownloadTimeout = %s", cfg.DownloadTimeout)
	}
	if cfg.PrepareTimeout != 12*time.Minute {
		t.Fatalf("PrepareTimeout = %s", cfg.PrepareTimeout)
	}
	if cfg.TelegramUploadRetries != 5 {
		t.Fatalf("TelegramUploadRetries = %d", cfg.TelegramUploadRetries)
	}
	if cfg.TelegramUploadTimeout != 3*time.Minute {
		t.Fatalf("TelegramUploadTimeout = %s", cfg.TelegramUploadTimeout)
	}
	if cfg.MaxVideoBytes != 123 {
		t.Fatalf("MaxVideoBytes = %d", cfg.MaxVideoBytes)
	}
	if cfg.HealthAddr != "127.0.0.1:9000" {
		t.Fatalf("HealthAddr = %q", cfg.HealthAddr)
	}
	if cfg.MetricsAddr != "127.0.0.1:10000" {
		t.Fatalf("MetricsAddr = %q", cfg.MetricsAddr)
	}
}

func TestLoadDecodesInstagramCookiesB64(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("INSTAGRAM_COOKIES_B64", base64.StdEncoding.EncodeToString([]byte("cookies-body")))
	t.Cleanup(func() {
		_ = os.Remove(instagramCookiesPath)
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.InstagramCookiesFile != instagramCookiesPath {
		t.Fatalf("InstagramCookiesFile = %q, want %q", cfg.InstagramCookiesFile, instagramCookiesPath)
	}

	body, err := os.ReadFile(cfg.InstagramCookiesFile)
	if err != nil {
		t.Fatalf("read cookies file: %v", err)
	}
	if string(body) != "cookies-body" {
		t.Fatalf("cookies body = %q", body)
	}
}

func TestLoadPrefersInstagramCookiesFileOverB64(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("INSTAGRAM_COOKIES_FILE", "/run/secrets/ig-cookies.txt")
	t.Setenv("INSTAGRAM_COOKIES_B64", base64.StdEncoding.EncodeToString([]byte("cookies-body")))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.InstagramCookiesFile != "/run/secrets/ig-cookies.txt" {
		t.Fatalf("InstagramCookiesFile = %q", cfg.InstagramCookiesFile)
	}
}

func TestLoadRejectsInvalidInstagramCookiesB64(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("INSTAGRAM_COOKIES_B64", "not-base64")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want base64 error")
	}
}

func TestLoadRequiresTelegramBotToken(t *testing.T) {
	_, err := Load()
	if !errors.Is(err, ErrMissingTelegramBotToken) {
		t.Fatalf("Load() error = %v, want %v", err, ErrMissingTelegramBotToken)
	}
}

func TestLoadRejectsInvalidTelegramStorageChatID(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_STORAGE_CHAT_ID", "chat")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want parse error")
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	tests := map[string]string{
		"DOWNLOAD_TIMEOUT":        "soon",
		"PREPARE_TIMEOUT":         "soon",
		"TELEGRAM_UPLOAD_TIMEOUT": "soon",
	}

	for key, value := range tests {
		t.Run(key, func(t *testing.T) {
			t.Setenv("TELEGRAM_BOT_TOKEN", "token")
			t.Setenv("TELEGRAM_STORAGE_CHAT_ID", "42")
			t.Setenv(key, value)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want parse error")
			}
		})
	}
}

func TestLoadRejectsInvalidTelegramUploadRetries(t *testing.T) {
	tests := []string{"bad", "-1"}

	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TELEGRAM_BOT_TOKEN", "token")
			t.Setenv("TELEGRAM_STORAGE_CHAT_ID", "42")
			t.Setenv("TELEGRAM_UPLOAD_RETRIES", value)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want parse or validation error")
			}
		})
	}
}

func TestLoadRejectsInvalidMaxVideoBytes(t *testing.T) {
	tests := []string{"0", "-1"}

	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TELEGRAM_BOT_TOKEN", "token")
			t.Setenv("TELEGRAM_STORAGE_CHAT_ID", "42")
			t.Setenv("MAX_VIDEO_BYTES", value)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want validation error")
			}
		})
	}
}
