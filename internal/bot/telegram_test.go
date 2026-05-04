package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

func TestRetryTelegramUploadSucceedsAfterThreeRetries(t *testing.T) {
	logger := &fakeLogger{}
	attempts := 0
	errBoom := errors.New("telegram timeout")

	err := retryTelegramUpload(context.Background(), telegramUploadRetryConfig{
		kind:    "video",
		retries: 3,
		timeout: time.Second,
		logger:  logger,
		backoff: func(int) time.Duration { return 0 },
		sleep:   func(time.Duration) {},
		operation: func(context.Context) error {
			attempts++
			if attempts < 4 {
				return errBoom
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("retryTelegramUpload() error = %v", err)
	}
	if attempts != 4 {
		t.Fatalf("attempts = %d, want 4", attempts)
	}
	if len(logger.errorMessages) != 3 {
		t.Fatalf("logged errors = %d, want 3", len(logger.errorMessages))
	}
	for _, msg := range logger.errorMessages {
		if msg != "telegram upload attempt failed" {
			t.Fatalf("log message = %q", msg)
		}
	}
}

func TestRetryTelegramUploadReturnsLastErrorAfterRetries(t *testing.T) {
	attempts := 0
	errLast := errors.New("last error")

	err := retryTelegramUpload(context.Background(), telegramUploadRetryConfig{
		kind:    "photo",
		retries: 3,
		timeout: time.Second,
		backoff: func(int) time.Duration { return 0 },
		sleep:   func(time.Duration) {},
		operation: func(context.Context) error {
			attempts++
			return errLast
		},
	})
	if !errors.Is(err, errLast) {
		t.Fatalf("retryTelegramUpload() error = %v, want %v", err, errLast)
	}
	if attempts != 4 {
		t.Fatalf("attempts = %d, want 4", attempts)
	}
}

func TestInputMediaDoesNotIncludeSourceCaption(t *testing.T) {
	videoMedia, err := inputMedia(CachedMedia{
		Kind:     MediaKindVideo,
		FileID:   "video-file-id",
		Duration: 12,
		Width:    720,
		Height:   1280,
	})
	if err != nil {
		t.Fatalf("inputMedia(video) error = %v", err)
	}
	video, ok := videoMedia.(gotgbot.InputMediaVideo)
	if !ok {
		t.Fatalf("inputMedia(video) = %T, want InputMediaVideo", videoMedia)
	}
	if video.Caption != "" {
		t.Fatalf("video caption = %q, want no inline caption", video.Caption)
	}

	photoMedia, err := inputMedia(CachedMedia{
		Kind:   MediaKindPhoto,
		FileID: "photo-file-id",
	})
	if err != nil {
		t.Fatalf("inputMedia(photo) error = %v", err)
	}
	photo, ok := photoMedia.(gotgbot.InputMediaPhoto)
	if !ok {
		t.Fatalf("inputMedia(photo) = %T, want InputMediaPhoto", photoMedia)
	}
	if photo.Caption != "" {
		t.Fatalf("photo caption = %q, want no inline caption", photo.Caption)
	}
}
