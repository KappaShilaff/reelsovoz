package bot

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

const (
	defaultTelegramUploadRetries = 3
	defaultTelegramUploadTimeout = 120 * time.Second
)

type TelegramClient interface {
	UploadVideo(ctx context.Context, chatID int64, video Media) (string, error)
	UploadPhoto(ctx context.Context, chatID int64, photo Media) (string, error)
	AnswerInlineQuery(ctx context.Context, queryID string, results []gotgbot.InlineQueryResult, opts *gotgbot.AnswerInlineQueryOpts) error
	EditInlineMessageMedia(ctx context.Context, inlineMessageID string, media CachedMedia) error
	EditInlineMessageText(ctx context.Context, inlineMessageID string, text string) error
	SendMessage(ctx context.Context, chatID int64, text string) error
}

type GotgbotTelegram struct {
	Bot           *gotgbot.Bot
	Logger        Logger
	UploadRetries int
	UploadTimeout time.Duration
}

func (c GotgbotTelegram) UploadVideo(ctx context.Context, chatID int64, video Media) (string, error) {
	if c.Bot == nil {
		return "", fmt.Errorf("telegram bot is nil")
	}
	if video.Filename == "" {
		video.Filename = "reelsovoz.mp4"
	}

	var fileID string
	err := c.retryUpload(ctx, "video", func(attemptCtx context.Context) error {
		msg, err := c.Bot.SendVideoWithContext(attemptCtx, chatID, gotgbot.InputFileByReader(video.Filename, bytes.NewReader(video.Bytes)), &gotgbot.SendVideoOpts{
			SupportsStreaming: true,
			Duration:          video.Duration,
			Width:             video.Width,
			Height:            video.Height,
			Caption:           video.Caption,
		})
		if err != nil {
			return fmt.Errorf("send video to storage chat: %w", err)
		}
		if msg == nil || msg.Video == nil || msg.Video.FileId == "" {
			return fmt.Errorf("send video to storage chat: response has no video file_id")
		}
		fileID = msg.Video.FileId
		return nil
	})
	return fileID, err
}

func (c GotgbotTelegram) UploadPhoto(ctx context.Context, chatID int64, photo Media) (string, error) {
	if c.Bot == nil {
		return "", fmt.Errorf("telegram bot is nil")
	}
	if photo.Filename == "" {
		photo.Filename = "reelsovoz.jpg"
	}

	var fileID string
	err := c.retryUpload(ctx, "photo", func(attemptCtx context.Context) error {
		msg, err := c.Bot.SendPhotoWithContext(attemptCtx, chatID, gotgbot.InputFileByReader(photo.Filename, bytes.NewReader(photo.Bytes)), &gotgbot.SendPhotoOpts{
			Caption: photo.Caption,
		})
		if err != nil {
			return fmt.Errorf("send photo to storage chat: %w", err)
		}

		if msg == nil {
			return fmt.Errorf("send photo to storage chat: response has no photo file_id")
		}
		fileID = largestPhotoFileID(msg.Photo)
		if fileID == "" {
			return fmt.Errorf("send photo to storage chat: response has no photo file_id")
		}
		return nil
	})
	return fileID, err
}

func (c GotgbotTelegram) retryUpload(parent context.Context, kind string, operation func(context.Context) error) error {
	return retryTelegramUpload(parent, telegramUploadRetryConfig{
		kind:      kind,
		retries:   c.uploadRetries(),
		timeout:   c.uploadTimeout(),
		logger:    c.Logger,
		backoff:   telegramUploadBackoff,
		sleep:     time.Sleep,
		operation: operation,
	})
}

type telegramUploadRetryConfig struct {
	kind      string
	retries   int
	timeout   time.Duration
	logger    Logger
	backoff   func(int) time.Duration
	sleep     func(time.Duration)
	operation func(context.Context) error
}

func retryTelegramUpload(parent context.Context, cfg telegramUploadRetryConfig) error {
	_ = parent
	if cfg.timeout <= 0 {
		cfg.timeout = defaultTelegramUploadTimeout
	}
	if cfg.retries < 0 {
		cfg.retries = 0
	}
	if cfg.backoff == nil {
		cfg.backoff = telegramUploadBackoff
	}
	if cfg.sleep == nil {
		cfg.sleep = time.Sleep
	}
	attempts := cfg.retries + 1

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
		err := cfg.operation(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt >= attempts {
			break
		}
		if cfg.logger != nil {
			cfg.logger.Error("telegram upload attempt failed", "error", err, "attempt", attempt, "max_attempts", attempts, "kind", cfg.kind)
		}
		delay := cfg.backoff(attempt)
		if delay <= 0 {
			continue
		}
		cfg.sleep(delay)
	}
	return lastErr
}

func telegramUploadBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func (c GotgbotTelegram) uploadRetries() int {
	if c.UploadRetries <= 0 {
		return defaultTelegramUploadRetries
	}
	return c.UploadRetries
}

func (c GotgbotTelegram) uploadTimeout() time.Duration {
	if c.UploadTimeout <= 0 {
		return defaultTelegramUploadTimeout
	}
	return c.UploadTimeout
}

func (c GotgbotTelegram) AnswerInlineQuery(ctx context.Context, queryID string, results []gotgbot.InlineQueryResult, opts *gotgbot.AnswerInlineQueryOpts) error {
	if c.Bot == nil {
		return fmt.Errorf("telegram bot is nil")
	}

	if _, err := c.Bot.AnswerInlineQueryWithContext(ctx, queryID, results, opts); err != nil {
		return fmt.Errorf("answer inline query: %w", err)
	}
	return nil
}

func (c GotgbotTelegram) EditInlineMessageMedia(ctx context.Context, inlineMessageID string, media CachedMedia) error {
	if c.Bot == nil {
		return fmt.Errorf("telegram bot is nil")
	}
	inputMedia, err := inputMedia(media)
	if err != nil {
		return err
	}
	if _, _, err := c.Bot.EditMessageMediaWithContext(ctx, inputMedia, &gotgbot.EditMessageMediaOpts{
		InlineMessageId: inlineMessageID,
	}); err != nil {
		return fmt.Errorf("edit inline message media: %w", err)
	}
	return nil
}

func (c GotgbotTelegram) EditInlineMessageText(ctx context.Context, inlineMessageID string, text string) error {
	if c.Bot == nil {
		return fmt.Errorf("telegram bot is nil")
	}
	if _, _, err := c.Bot.EditMessageTextWithContext(ctx, text, &gotgbot.EditMessageTextOpts{
		InlineMessageId: inlineMessageID,
	}); err != nil {
		return fmt.Errorf("edit inline message text: %w", err)
	}
	return nil
}

func (c GotgbotTelegram) SendMessage(ctx context.Context, chatID int64, text string) error {
	if c.Bot == nil {
		return fmt.Errorf("telegram bot is nil")
	}
	if _, err := c.Bot.SendMessageWithContext(ctx, chatID, text, nil); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}

func inputMedia(media CachedMedia) (gotgbot.InputMedia, error) {
	switch media.Kind {
	case MediaKindVideo:
		return gotgbot.InputMediaVideo{
			Media:             gotgbot.InputFileByID(media.FileID),
			Duration:          media.Duration,
			Width:             media.Width,
			Height:            media.Height,
			SupportsStreaming: true,
		}, nil
	case MediaKindPhoto:
		return gotgbot.InputMediaPhoto{
			Media: gotgbot.InputFileByID(media.FileID),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported media kind %q", media.Kind)
	}
}

func largestPhotoFileID(photos []gotgbot.PhotoSize) string {
	var best gotgbot.PhotoSize
	for _, photo := range photos {
		if photo.FileId == "" {
			continue
		}
		if best.FileId == "" || photo.Width*photo.Height > best.Width*best.Height {
			best = photo
		}
	}
	return best.FileId
}
