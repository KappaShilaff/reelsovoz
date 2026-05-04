package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultYTDLPPath     = "yt-dlp"
	defaultFFmpegPath    = "ffmpeg"
	defaultDownloadTTL   = 90 * time.Second
	defaultPrepareTTL    = 10 * time.Minute
	defaultUploadRetries = 3
	defaultUploadTimeout = 120 * time.Second
	defaultMaxVideoBytes = int64(50_331_648)
	defaultHealthAddr    = ":8000"
	defaultMetricsAddr   = ":10000"
	defaultUserStorage   = "/data/reelsovoz-users.json"
	instagramCookiesPath = "/tmp/reelsovoz-instagram-cookies.txt"
)

var (
	ErrMissingTelegramBotToken = errors.New("TELEGRAM_BOT_TOKEN is required")
)

type Config struct {
	TelegramBotToken      string
	TelegramStorageChatID int64
	UserStorageFile       string
	YTDLPPath             string
	FFmpegPath            string
	InstagramCookiesFile  string
	DownloadTimeout       time.Duration
	PrepareTimeout        time.Duration
	TelegramUploadRetries int
	TelegramUploadTimeout time.Duration
	MaxVideoBytes         int64
	HealthAddr            string
	MetricsAddr           string
}

func Load() (Config, error) {
	var cfg Config

	cfg.TelegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if cfg.TelegramBotToken == "" {
		return Config{}, ErrMissingTelegramBotToken
	}

	if storageChatID := os.Getenv("TELEGRAM_STORAGE_CHAT_ID"); storageChatID != "" {
		parsedStorageChatID, err := strconv.ParseInt(storageChatID, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse TELEGRAM_STORAGE_CHAT_ID: %w", err)
		}
		cfg.TelegramStorageChatID = parsedStorageChatID
	}
	cfg.UserStorageFile = envOrDefault("USER_STORAGE_FILE", defaultUserStorage)

	cfg.YTDLPPath = envOrDefault("YT_DLP_PATH", defaultYTDLPPath)
	cfg.FFmpegPath = envOrDefault("FFMPEG_PATH", defaultFFmpegPath)
	var err error
	cfg.InstagramCookiesFile, err = InstagramCookiesFileFromEnv()
	if err != nil {
		return Config{}, err
	}

	cfg.DownloadTimeout, err = durationEnvOrDefault("DOWNLOAD_TIMEOUT", defaultDownloadTTL)
	if err != nil {
		return Config{}, err
	}
	cfg.PrepareTimeout, err = durationEnvOrDefault("PREPARE_TIMEOUT", defaultPrepareTTL)
	if err != nil {
		return Config{}, err
	}
	cfg.TelegramUploadTimeout, err = durationEnvOrDefault("TELEGRAM_UPLOAD_TIMEOUT", defaultUploadTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.TelegramUploadRetries, err = intEnvOrDefault("TELEGRAM_UPLOAD_RETRIES", defaultUploadRetries)
	if err != nil {
		return Config{}, err
	}
	if cfg.TelegramUploadRetries < 0 {
		return Config{}, fmt.Errorf("TELEGRAM_UPLOAD_RETRIES must be non-negative")
	}

	maxVideoBytes := os.Getenv("MAX_VIDEO_BYTES")
	if maxVideoBytes == "" {
		cfg.MaxVideoBytes = defaultMaxVideoBytes
	} else {
		cfg.MaxVideoBytes, err = strconv.ParseInt(maxVideoBytes, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse MAX_VIDEO_BYTES: %w", err)
		}
	}
	if cfg.MaxVideoBytes <= 0 {
		return Config{}, fmt.Errorf("MAX_VIDEO_BYTES must be positive")
	}

	cfg.HealthAddr = envOrDefault("HEALTH_ADDR", defaultHealthAddr)
	cfg.MetricsAddr = envOrDefault("METRICS_ADDR", defaultMetricsAddr)

	return cfg, nil
}

func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func durationEnvOrDefault(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func intEnvOrDefault(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func InstagramCookiesFileFromEnv() (string, error) {
	path := os.Getenv("INSTAGRAM_COOKIES_FILE")
	if path != "" {
		return path, nil
	}

	encoded := os.Getenv("INSTAGRAM_COOKIES_B64")
	if encoded == "" {
		return "", nil
	}

	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode INSTAGRAM_COOKIES_B64: %w", err)
	}
	if len(body) == 0 {
		return "", nil
	}
	if err := os.WriteFile(instagramCookiesPath, body, 0o600); err != nil {
		return "", fmt.Errorf("write Instagram cookies file: %w", err)
	}
	return instagramCookiesPath, nil
}
