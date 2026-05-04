package main

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/choseninlineresult"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/inlinequery"

	"github.com/KappaShilaff/reelsovoz/internal/bot"
	"github.com/KappaShilaff/reelsovoz/internal/config"
	"github.com/KappaShilaff/reelsovoz/internal/health"
	"github.com/KappaShilaff/reelsovoz/internal/logging"
	"github.com/KappaShilaff/reelsovoz/internal/reels"
)

func main() {
	logger := logging.New(os.Stdout)

	cfg, err := config.Load()
	if err != nil {
		exitWithError(logger, "load config", err)
	}
	if _, err := exec.LookPath(cfg.YTDLPPath); err != nil {
		exitWithError(logger, "find yt-dlp executable", err, "path", cfg.YTDLPPath)
	}
	if _, err := exec.LookPath(cfg.FFmpegPath); err != nil {
		exitWithError(logger, "find ffmpeg executable", err, "path", cfg.FFmpegPath)
	}
	storageRegistry, err := bot.LoadUserStorageRegistry(cfg.UserStorageFile)
	if err != nil {
		exitWithError(logger, "load user storage registry", err, "path", cfg.UserStorageFile)
	}

	telegramBot, err := gotgbot.NewBot(cfg.TelegramBotToken, nil)
	if err != nil {
		exitWithError(logger, "create telegram bot", err)
	}

	health.Start(context.Background(), cfg.HealthAddr, logger)

	dispatcher := ext.NewDispatcher(&ext.DispatcherOpts{
		Error: func(_ *gotgbot.Bot, _ *ext.Context, err error) ext.DispatcherAction {
			logger.Error("telegram update failed", "error", err)
			return ext.DispatcherActionNoop
		},
		Logger: logger,
	})
	updater := ext.NewUpdater(dispatcher, &ext.UpdaterOpts{Logger: logger})

	inlineHandler := bot.InlineHandler{
		Service: bot.YTDLPService{Downloader: reels.Downloader{
			YTDLPPath:            cfg.YTDLPPath,
			FFmpegPath:           cfg.FFmpegPath,
			InstagramCookiesFile: cfg.InstagramCookiesFile,
			Timeout:              cfg.DownloadTimeout,
			MaxBytes:             cfg.MaxVideoBytes,
		}},
		Logger:          logger,
		StorageChatID:   cfg.TelegramStorageChatID,
		StorageRegistry: storageRegistry,
		PrepareTimeout:  cfg.PrepareTimeout,
		UploadRetries:   cfg.TelegramUploadRetries,
		UploadTimeout:   cfg.TelegramUploadTimeout,
	}
	dispatcher.AddHandler(handlers.NewCommand("start", inlineHandler.HandleStartGotgbot))
	dispatcher.AddHandler(handlers.NewInlineQuery(inlinequery.All, inlineHandler.HandleGotgbot))
	dispatcher.AddHandler(handlers.NewChosenInlineResult(choseninlineresult.All, inlineHandler.HandleChosenGotgbot))

	if err := updater.StartPolling(telegramBot, &ext.PollingOpts{
		DropPendingUpdates:    true,
		EnableWebhookDeletion: true,
		GetUpdatesOpts: &gotgbot.GetUpdatesOpts{
			Timeout:        9,
			AllowedUpdates: []string{"message", "inline_query", "chosen_inline_result"},
			RequestOpts: &gotgbot.RequestOpts{
				Timeout: 10 * time.Second,
			},
		},
	}); err != nil {
		exitWithError(logger, "start telegram polling", err)
	}

	logger.Info("telegram bot started", "username", telegramBot.User.Username)
	updater.Idle()
}

func exitWithError(logger interface {
	Error(msg string, args ...any)
}, message string, err error, args ...any) {
	fields := append([]any{"error", err}, args...)
	logger.Error(message, fields...)
	os.Exit(1)
}
