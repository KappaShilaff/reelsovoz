package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"github.com/KappaShilaff/reelsovoz/internal/reels"
)

type InlineHandler struct {
	Service         ReelsService
	Telegram        TelegramClient
	Logger          Logger
	Cache           *MediaCache
	StorageChatID   int64
	StorageRegistry *UserStorageRegistry
	PrepareTimeout  time.Duration
	UploadRetries   int
	UploadTimeout   time.Duration
	RunBackground   func(func())
	Metrics         MetricsRecorder
}

type Logger interface {
	Error(msg string, args ...any)
	Info(msg string, args ...any)
}

type MetricsRecorder interface {
	IncStartCommand(result string)
	IncInlineQuery(result string, platform string)
	IncInlineChosen(result string, kind string, source string, platform string)
	ObserveDownload(platform string, status string, duration time.Duration)
	IncDownloadedMedia(platform string, kind string)
	ObserveDownloadedMediaSize(platform string, kind string, sizeBytes int)
	ObserveStorageUpload(kind string, status string, duration time.Duration)
	ObserveStorageUploadSize(kind string, sizeBytes int)
	IncInlineMediaSent(kind string, source string, status string)
}

type InlineQuery struct {
	ID         string
	Query      string
	FromUserID int64
}

type ChosenInlineResult struct {
	ResultID        string
	Query           string
	InlineMessageID string
	FromUserID      int64
}

type StartCommand struct {
	UserID   int64
	ChatID   int64
	ChatType string
}

func (h InlineHandler) Handle(ctx context.Context, query InlineQuery) error {
	reelURL, err := reels.ExtractURL(strings.TrimSpace(query.Query))
	if err != nil {
		h.recordInlineQuery("usage", "unknown")
		return h.answerUsage(ctx, query.ID)
	}
	reelURL = withDefaultScheme(reelURL)
	platform := sourcePlatform(reelURL)
	storageChatID, ok := h.storageChatIDForUser(query.FromUserID)
	if !ok {
		h.recordInlineQuery("start_required", platform)
		return h.answerStartRequired(ctx, query.ID)
	}
	cacheKey := h.mediaCacheKeyForUser(reelURL, query.FromUserID)
	cache := h.mediaCache()
	if deleted := cache.CleanupExpired(); deleted > 0 {
		h.logInfo("inline media cache cleanup", reelURL, "deleted", deleted)
	}
	if cached, ok := cache.Get(cacheKey); ok {
		h.logInfo("inline media cache hit", reelURL, "items", len(cached))
		h.recordInlineQuery("cache_hit", platform)
		return h.answerCachedMedia(ctx, query.ID, cached)
	}
	h.logInfo("inline media cache miss", reelURL)

	started := cache.StartPrepare(cacheKey)
	if started {
		h.logInfo("inline media prepare queued", reelURL)
		h.recordInlineQuery("cache_miss_queued", platform)
		defer h.runBackground(func() {
			h.prepareInlineMedia(reelURL, cacheKey, storageChatID)
		})
	} else {
		h.logInfo("inline media prepare already running", reelURL)
		h.recordInlineQuery("cache_miss_inflight", platform)
	}
	return h.answerPreparing(ctx, query.ID)
}

func (h InlineHandler) HandleChosen(ctx context.Context, chosen ChosenInlineResult) error {
	if chosen.InlineMessageID == "" {
		h.logError("chosen inline result has no inline_message_id", fmt.Errorf("inline feedback disabled or missing inline keyboard"), chosen.Query, "result_id", chosen.ResultID)
		h.recordInlineChosen("missing_inline_message_id", "unknown", "unknown", sourcePlatform(chosen.Query))
		return nil
	}
	reelURL, err := reels.ExtractURL(strings.TrimSpace(chosen.Query))
	if err != nil {
		h.recordInlineChosen("bad_query", "unknown", "unknown", "unknown")
		return h.Telegram.EditInlineMessageText(ctx, chosen.InlineMessageID, "Could not find a supported URL in this inline query.")
	}
	reelURL = withDefaultScheme(reelURL)
	platform := sourcePlatform(reelURL)
	storageChatID, ok := h.storageChatIDForUser(chosen.FromUserID)
	if !ok {
		h.recordInlineChosen("start_required", "unknown", "unknown", platform)
		return h.Telegram.EditInlineMessageText(ctx, chosen.InlineMessageID, "Open @ReelsovozBot and press Start first.")
	}
	cacheKey := h.mediaCacheKeyForUser(reelURL, chosen.FromUserID)
	cache := h.mediaCache()
	if cached, ok := cache.Get(cacheKey); ok {
		kind := firstCachedKind(cached)
		h.recordInlineChosen("cache_hit", kind, "cache", platform)
		err := h.editInlineMessageToCachedMedia(ctx, chosen.InlineMessageID, cached)
		h.recordInlineMediaSent(kind, "cache", statusFromError(ctx, err))
		return err
	}
	cache.AddWaiter(cacheKey, chosen.InlineMessageID)
	if cache.StartPrepare(cacheKey) {
		h.logInfo("inline media prepare queued from chosen result", reelURL)
		h.recordInlineChosen("prepare_queued", "unknown", "unknown", platform)
		h.runBackground(func() {
			h.prepareInlineMedia(reelURL, cacheKey, storageChatID)
		})
	} else {
		h.recordInlineChosen("waiting", "unknown", "unknown", platform)
	}
	return nil
}

func (h InlineHandler) HandleStart(ctx context.Context, cmd StartCommand) error {
	if h.Telegram == nil {
		h.recordStartCommand("error")
		return fmt.Errorf("telegram client is nil")
	}
	if cmd.ChatType != "" && cmd.ChatType != "private" {
		h.recordStartCommand("non_private")
		return h.Telegram.SendMessage(ctx, cmd.ChatID, "Open a private chat with @ReelsovozBot and press Start there.")
	}
	if h.StorageRegistry == nil {
		h.recordStartCommand("not_configured")
		return h.Telegram.SendMessage(ctx, cmd.ChatID, "Storage registration is not configured for this bot.")
	}
	if err := h.StorageRegistry.Register(cmd.UserID, cmd.ChatID); err != nil {
		h.recordStartCommand("error")
		return err
	}
	h.recordStartCommand("success")
	return h.Telegram.SendMessage(ctx, cmd.ChatID, "Storage registered. Now you can use @ReelsovozBot inline mode.")
}

func (h InlineHandler) storageChatIDForUser(userID int64) (int64, bool) {
	if h.StorageRegistry != nil {
		if storage, ok := h.StorageRegistry.Get(userID); ok {
			return storage.ChatID, true
		}
	}
	if h.StorageChatID != 0 {
		return h.StorageChatID, true
	}
	return 0, false
}

func (h InlineHandler) runBackground(fn func()) {
	if h.RunBackground != nil {
		h.RunBackground(fn)
		return
	}
	go fn()
}

func (h InlineHandler) prepareInlineMedia(reelURL string, cacheKey string, storageChatID int64) {
	cache := h.mediaCache()
	defer cache.FinishPrepare(cacheKey)

	ctx, cancel := context.WithTimeout(context.Background(), h.prepareTimeout())
	defer cancel()

	platform := sourcePlatform(reelURL)
	downloadStarted := time.Now()
	media, err := h.Service.Download(ctx, reelURL)
	h.recordDownload(platform, statusFromError(ctx, err), time.Since(downloadStarted))
	if err != nil {
		h.logError("download inline media failed", err, reelURL)
		h.editPendingInlineMessages(ctx, cacheKey, nil, "Could not download this media.")
		return
	}
	if len(media) == 0 {
		h.logError("download inline media returned no supported media", fmt.Errorf("empty media result"), reelURL)
		h.editPendingInlineMessages(ctx, cacheKey, nil, "This reel has no supported media.")
		return
	}

	for _, item := range media {
		h.recordDownloadedMedia(platform, string(item.Kind), len(item.Bytes))
	}

	cached := make([]CachedMedia, 0, len(media))
	for i, item := range media {
		cachedItem, _, err := h.uploadAndBuildResult(ctx, reelURL, i, item, storageChatID)
		if err != nil {
			h.logError("upload inline media failed", err, reelURL, "kind", item.Kind, "index", i)
			h.editPendingInlineMessages(ctx, cacheKey, nil, "Could not upload this media.")
			return
		}
		cached = append(cached, cachedItem)
	}
	cache.Set(cacheKey, cached)
	h.logInfo("inline media cache stored", reelURL, "items", len(cached))
	h.editPendingInlineMessages(ctx, cacheKey, cached, "")
}

func (h InlineHandler) editPendingInlineMessages(ctx context.Context, cacheKey string, media []CachedMedia, errorText string) {
	waiters := h.mediaCache().TakeWaiters(cacheKey)
	for _, inlineMessageID := range waiters {
		if errorText != "" {
			if err := h.Telegram.EditInlineMessageText(ctx, inlineMessageID, errorText); err != nil {
				h.logError("edit pending inline message text failed", err, "", "inline_message_id", inlineMessageID)
			}
			continue
		}
		kind := firstCachedKind(media)
		if err := h.editInlineMessageToCachedMedia(ctx, inlineMessageID, media); err != nil {
			h.recordInlineMediaSent(kind, "async", "error")
			h.logError("edit pending inline message media failed", err, "", "inline_message_id", inlineMessageID)
			continue
		}
		h.recordInlineMediaSent(kind, "async", "success")
	}
}

func (h InlineHandler) editInlineMessageToCachedMedia(ctx context.Context, inlineMessageID string, media []CachedMedia) error {
	if len(media) == 0 {
		return h.Telegram.EditInlineMessageText(ctx, inlineMessageID, "This reel has no supported media.")
	}
	return h.Telegram.EditInlineMessageMedia(ctx, inlineMessageID, media[0])
}

func (h InlineHandler) prepareTimeout() time.Duration {
	if h.PrepareTimeout > 0 {
		return h.PrepareTimeout
	}
	return 3 * time.Minute
}

func (h InlineHandler) mediaCache() *MediaCache {
	if h.Cache != nil {
		return h.Cache
	}
	return defaultMediaCache()
}

func (h InlineHandler) logError(message string, err error, rawURL string, args ...any) {
	if h.Logger == nil {
		return
	}
	fields := []any{"error", err}
	if host, path := urlParts(rawURL); host != "" {
		fields = append(fields, "url_host", host, "url_path", path)
	}
	fields = append(fields, args...)
	h.Logger.Error(message, fields...)
}

func (h InlineHandler) logInfo(message string, rawURL string, args ...any) {
	if h.Logger == nil {
		return
	}
	fields := []any{}
	if host, path := urlParts(rawURL); host != "" {
		fields = append(fields, "url_host", host, "url_path", path)
	}
	fields = append(fields, args...)
	h.Logger.Info(message, fields...)
}

func (h InlineHandler) uploadAndBuildResult(ctx context.Context, reelURL string, index int, media Media, storageChatID int64) (CachedMedia, gotgbot.InlineQueryResult, error) {
	caption := sourceCaption(reelURL)
	media.Caption = caption
	cached := CachedMedia{
		Kind:        media.Kind,
		ResultID:    resultID(reelURL, index, media.Kind),
		Title:       resultTitle(media.Kind, index),
		Description: reelURL,
		Duration:    media.Duration,
		Width:       media.Width,
		Height:      media.Height,
	}
	switch media.Kind {
	case MediaKindVideo:
		h.recordStorageUploadSize(string(media.Kind), len(media.Bytes))
		started := time.Now()
		fileID, err := h.Telegram.UploadVideo(ctx, storageChatID, media)
		h.recordStorageUpload(string(media.Kind), statusFromError(ctx, err), time.Since(started))
		if err != nil {
			return CachedMedia{}, nil, err
		}
		cached.FileID = fileID
		return cached, gotgbot.InlineQueryResultCachedVideo{
			Id:          cached.ResultID,
			VideoFileId: fileID,
			Title:       cached.Title,
			Description: cached.Description,
		}, nil
	case MediaKindPhoto:
		h.recordStorageUploadSize(string(media.Kind), len(media.Bytes))
		started := time.Now()
		fileID, err := h.Telegram.UploadPhoto(ctx, storageChatID, media)
		h.recordStorageUpload(string(media.Kind), statusFromError(ctx, err), time.Since(started))
		if err != nil {
			return CachedMedia{}, nil, err
		}
		cached.FileID = fileID
		return cached, gotgbot.InlineQueryResultCachedPhoto{
			Id:          cached.ResultID,
			PhotoFileId: fileID,
			Title:       cached.Title,
			Description: cached.Description,
		}, nil
	default:
		return CachedMedia{}, nil, fmt.Errorf("unsupported media kind %q", media.Kind)
	}
}

func (h InlineHandler) recordStartCommand(result string) {
	if h.Metrics != nil {
		h.Metrics.IncStartCommand(result)
	}
}

func (h InlineHandler) recordInlineQuery(result string, platform string) {
	if h.Metrics != nil {
		h.Metrics.IncInlineQuery(result, platform)
	}
}

func (h InlineHandler) recordInlineChosen(result string, kind string, source string, platform string) {
	if h.Metrics != nil {
		h.Metrics.IncInlineChosen(result, kind, source, platform)
	}
}

func (h InlineHandler) recordDownload(platform string, status string, duration time.Duration) {
	if h.Metrics != nil {
		h.Metrics.ObserveDownload(platform, status, duration)
	}
}

func (h InlineHandler) recordDownloadedMedia(platform string, kind string, sizeBytes int) {
	if h.Metrics == nil {
		return
	}
	h.Metrics.IncDownloadedMedia(platform, kind)
	h.Metrics.ObserveDownloadedMediaSize(platform, kind, sizeBytes)
}

func (h InlineHandler) recordStorageUpload(kind string, status string, duration time.Duration) {
	if h.Metrics != nil {
		h.Metrics.ObserveStorageUpload(kind, status, duration)
	}
}

func (h InlineHandler) recordStorageUploadSize(kind string, sizeBytes int) {
	if h.Metrics != nil {
		h.Metrics.ObserveStorageUploadSize(kind, sizeBytes)
	}
}

func (h InlineHandler) recordInlineMediaSent(kind string, source string, status string) {
	if h.Metrics != nil {
		h.Metrics.IncInlineMediaSent(kind, source, status)
	}
}

func (h InlineHandler) answerCachedMedia(ctx context.Context, queryID string, media []CachedMedia) error {
	results := make([]gotgbot.InlineQueryResult, 0, len(media))
	for _, item := range media {
		result, err := cachedResult(item)
		if err != nil {
			return h.answerError(ctx, queryID, "Could not use cached media")
		}
		results = append(results, result)
	}
	cacheTime := int64(0)
	return h.Telegram.AnswerInlineQuery(ctx, queryID, results, &gotgbot.AnswerInlineQueryOpts{
		IsPersonal: true,
		CacheTime:  &cacheTime,
	})
}

func cachedResult(item CachedMedia) (gotgbot.InlineQueryResult, error) {
	switch item.Kind {
	case MediaKindVideo:
		return gotgbot.InlineQueryResultCachedVideo{
			Id:          item.ResultID,
			VideoFileId: item.FileID,
			Title:       item.Title,
			Description: item.Description,
		}, nil
	case MediaKindPhoto:
		return gotgbot.InlineQueryResultCachedPhoto{
			Id:          item.ResultID,
			PhotoFileId: item.FileID,
			Title:       item.Title,
			Description: item.Description,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported media kind %q", item.Kind)
	}
}

func (h InlineHandler) HandleGotgbot(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.InlineQuery == nil {
		return nil
	}

	handler := h
	if handler.Telegram == nil {
		handler.Telegram = GotgbotTelegram{
			Bot:           b,
			Logger:        handler.Logger,
			UploadRetries: handler.UploadRetries,
			UploadTimeout: handler.UploadTimeout,
		}
	}

	return handler.Handle(context.Background(), InlineQuery{
		ID:         ctx.InlineQuery.Id,
		Query:      ctx.InlineQuery.Query,
		FromUserID: ctx.InlineQuery.From.Id,
	})
}

func (h InlineHandler) HandleChosenGotgbot(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.ChosenInlineResult == nil {
		return nil
	}

	handler := h
	if handler.Telegram == nil {
		handler.Telegram = GotgbotTelegram{
			Bot:           b,
			Logger:        handler.Logger,
			UploadRetries: handler.UploadRetries,
			UploadTimeout: handler.UploadTimeout,
		}
	}

	return handler.HandleChosen(context.Background(), ChosenInlineResult{
		ResultID:        ctx.ChosenInlineResult.ResultId,
		Query:           ctx.ChosenInlineResult.Query,
		InlineMessageID: ctx.ChosenInlineResult.InlineMessageId,
		FromUserID:      ctx.ChosenInlineResult.From.Id,
	})
}

func (h InlineHandler) HandleStartGotgbot(b *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.EffectiveMessage == nil || ctx.EffectiveUser == nil || ctx.EffectiveChat == nil {
		return nil
	}

	handler := h
	if handler.Telegram == nil {
		handler.Telegram = GotgbotTelegram{
			Bot:           b,
			Logger:        handler.Logger,
			UploadRetries: handler.UploadRetries,
			UploadTimeout: handler.UploadTimeout,
		}
	}

	return handler.HandleStart(context.Background(), StartCommand{
		UserID:   ctx.EffectiveUser.Id,
		ChatID:   ctx.EffectiveChat.Id,
		ChatType: ctx.EffectiveChat.Type,
	})
}

func (h InlineHandler) answerUsage(ctx context.Context, queryID string) error {
	return h.answerArticle(ctx, queryID, "usage", "Paste a reel URL", "Use inline mode with a full http(s) URL.", "Paste a reel URL after the bot username.")
}

func (h InlineHandler) answerError(ctx context.Context, queryID string, message string) error {
	return h.answerArticle(ctx, queryID, "error", message, "Try another TikTok or Instagram URL.", message)
}

func (h InlineHandler) answerStartRequired(ctx context.Context, queryID string) error {
	return h.answerArticle(ctx, queryID, "start-required", "Start bot first", "Open @ReelsovozBot and press Start to register private storage.", "Open @ReelsovozBot and press Start first.")
}

func (h InlineHandler) answerPreparing(ctx context.Context, queryID string) error {
	cacheTime := int64(0)
	return h.Telegram.AnswerInlineQuery(ctx, queryID, []gotgbot.InlineQueryResult{
		gotgbot.InlineQueryResultArticle{
			Id:          "preparing",
			Title:       "Send when ready",
			Description: "The message will turn into media after download finishes.",
			InputMessageContent: gotgbot.InputTextMessageContent{
				MessageText: "Preparing media...",
			},
			ReplyMarkup: &gotgbot.InlineKeyboardMarkup{
				InlineKeyboard: [][]gotgbot.InlineKeyboardButton{{
					{Text: "Preparing", CallbackData: "noop"},
				}},
			},
		},
	}, &gotgbot.AnswerInlineQueryOpts{
		IsPersonal: true,
		CacheTime:  &cacheTime,
	})
}

func (h InlineHandler) answerArticle(ctx context.Context, queryID string, id string, title string, description string, message string) error {
	cacheTime := int64(0)
	return h.Telegram.AnswerInlineQuery(ctx, queryID, []gotgbot.InlineQueryResult{
		gotgbot.InlineQueryResultArticle{
			Id:          id,
			Title:       title,
			Description: description,
			InputMessageContent: gotgbot.InputTextMessageContent{
				MessageText: message,
			},
		},
	}, &gotgbot.AnswerInlineQueryOpts{
		IsPersonal: true,
		CacheTime:  &cacheTime,
	})
}

func resultID(raw string, index int, kind MediaKind) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", raw, index, kind)))
	return hex.EncodeToString(sum[:])[:32]
}

func resultTitle(kind MediaKind, index int) string {
	if index == 0 {
		switch kind {
		case MediaKindPhoto:
			return "Send photo"
		default:
			return "Send reel"
		}
	}
	switch kind {
	case MediaKindPhoto:
		return fmt.Sprintf("Send photo %d", index+1)
	default:
		return fmt.Sprintf("Send reel %d", index+1)
	}
}

func sourceCaption(raw string) string {
	const maxCaptionLength = 1024
	caption := "Source: " + raw
	runes := []rune(caption)
	if len(runes) <= maxCaptionLength {
		return caption
	}
	return string(runes[:maxCaptionLength])
}

func withDefaultScheme(raw string) string {
	if strings.Contains(raw, "://") {
		return raw
	}
	return "https://" + raw
}

func urlParts(raw string) (string, string) {
	value := raw
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", ""
	}
	return parsed.Hostname(), parsed.EscapedPath()
}

func sourcePlatform(raw string) string {
	host, _ := urlParts(raw)
	switch {
	case strings.Contains(host, "instagram.com"):
		return "instagram"
	case strings.Contains(host, "tiktok.com"):
		return "tiktok"
	default:
		return "unknown"
	}
}

func statusFromError(ctx context.Context, err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func firstCachedKind(media []CachedMedia) string {
	if len(media) == 0 {
		return "unknown"
	}
	return string(media[0].Kind)
}

func mediaCacheKey(raw string) string {
	value := raw
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return raw
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func (h InlineHandler) mediaCacheKeyForUser(raw string, userID int64) string {
	key := mediaCacheKey(raw)
	if h.StorageRegistry == nil {
		return key
	}
	return fmt.Sprintf("%d:%s", userID, key)
}
