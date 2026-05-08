package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

type fakeReelsService struct {
	calls []string
	media []Media
	err   error
}

func (s *fakeReelsService) Download(_ context.Context, reelURL string) ([]Media, error) {
	s.calls = append(s.calls, reelURL)
	if s.err != nil {
		return nil, s.err
	}
	return s.media, nil
}

type fakeTelegramClient struct {
	videoUploads []uploadCall
	photoUploads []uploadCall
	videoFileID  string
	photoFileID  string

	answerQueryID string
	answerResults []gotgbot.InlineQueryResult
	answerOpts    *gotgbot.AnswerInlineQueryOpts
	mediaEdits    []mediaEditCall
	textEdits     []textEditCall
	messages      []sendMessageCall
}

type fakeLogger struct {
	errorMessages []string
	errorFields   [][]any
	infoMessages  []string
	infoFields    [][]any
}

type fakeMetricsRecorder struct {
	startResults  []string
	inlineQueries []inlineQueryMetric
	inlineChosen  []inlineChosenMetric
	downloads     []downloadMetric
	downloaded    []mediaMetric
	downloadSizes []mediaMetric
	uploadSizes   []mediaMetric
	uploads       []uploadMetric
	sent          []sentMetric
}

type inlineQueryMetric struct {
	result   string
	platform string
}

type inlineChosenMetric struct {
	result   string
	kind     string
	source   string
	platform string
}

type downloadMetric struct {
	platform string
	status   string
}

type mediaMetric struct {
	platform string
	kind     string
	size     int
}

type uploadMetric struct {
	kind   string
	status string
}

type sentMetric struct {
	kind   string
	source string
	status string
}

func (l *fakeLogger) Error(msg string, args ...any) {
	l.errorMessages = append(l.errorMessages, msg)
	l.errorFields = append(l.errorFields, args)
}

func (l *fakeLogger) Info(msg string, args ...any) {
	l.infoMessages = append(l.infoMessages, msg)
	l.infoFields = append(l.infoFields, args)
}

func (m *fakeMetricsRecorder) IncStartCommand(result string) {
	m.startResults = append(m.startResults, result)
}

func (m *fakeMetricsRecorder) IncInlineQuery(result string, platform string) {
	m.inlineQueries = append(m.inlineQueries, inlineQueryMetric{result: result, platform: platform})
}

func (m *fakeMetricsRecorder) IncInlineChosen(result string, kind string, source string, platform string) {
	m.inlineChosen = append(m.inlineChosen, inlineChosenMetric{result: result, kind: kind, source: source, platform: platform})
}

func (m *fakeMetricsRecorder) ObserveDownload(platform string, status string, _ time.Duration) {
	m.downloads = append(m.downloads, downloadMetric{platform: platform, status: status})
}

func (m *fakeMetricsRecorder) IncDownloadedMedia(platform string, kind string) {
	m.downloaded = append(m.downloaded, mediaMetric{platform: platform, kind: kind})
}

func (m *fakeMetricsRecorder) ObserveDownloadedMediaSize(platform string, kind string, sizeBytes int) {
	m.downloadSizes = append(m.downloadSizes, mediaMetric{platform: platform, kind: kind, size: sizeBytes})
}

func (m *fakeMetricsRecorder) ObserveStorageUpload(kind string, status string, _ time.Duration) {
	m.uploads = append(m.uploads, uploadMetric{kind: kind, status: status})
}

func (m *fakeMetricsRecorder) ObserveStorageUploadSize(kind string, sizeBytes int) {
	m.uploadSizes = append(m.uploadSizes, mediaMetric{kind: kind, size: sizeBytes})
}

func (m *fakeMetricsRecorder) IncInlineMediaSent(kind string, source string, status string) {
	m.sent = append(m.sent, sentMetric{kind: kind, source: source, status: status})
}

type uploadCall struct {
	chatID int64
	media  Media
}

type mediaEditCall struct {
	inlineMessageID string
	media           CachedMedia
}

type textEditCall struct {
	inlineMessageID string
	text            string
}

type sendMessageCall struct {
	chatID int64
	text   string
}

type capturedRunner struct {
	tasks []func()
}

func (r *capturedRunner) run(fn func()) {
	r.tasks = append(r.tasks, fn)
}

func (r *capturedRunner) runNext(t *testing.T) {
	t.Helper()
	if len(r.tasks) == 0 {
		t.Fatal("no background task queued")
	}
	task := r.tasks[0]
	r.tasks = r.tasks[1:]
	task()
}

func (c *fakeTelegramClient) UploadVideo(_ context.Context, chatID int64, video Media) (string, error) {
	c.videoUploads = append(c.videoUploads, uploadCall{chatID: chatID, media: video})
	return c.videoFileID, nil
}

func (c *fakeTelegramClient) UploadPhoto(_ context.Context, chatID int64, photo Media) (string, error) {
	c.photoUploads = append(c.photoUploads, uploadCall{chatID: chatID, media: photo})
	return c.photoFileID, nil
}

func (c *fakeTelegramClient) AnswerInlineQuery(_ context.Context, queryID string, results []gotgbot.InlineQueryResult, opts *gotgbot.AnswerInlineQueryOpts) error {
	c.answerQueryID = queryID
	c.answerResults = results
	c.answerOpts = opts
	return nil
}

func (c *fakeTelegramClient) EditInlineMessageMedia(_ context.Context, inlineMessageID string, media CachedMedia) error {
	c.mediaEdits = append(c.mediaEdits, mediaEditCall{inlineMessageID: inlineMessageID, media: media})
	return nil
}

func (c *fakeTelegramClient) EditInlineMessageText(_ context.Context, inlineMessageID string, text string) error {
	c.textEdits = append(c.textEdits, textEditCall{inlineMessageID: inlineMessageID, text: text})
	return nil
}

func (c *fakeTelegramClient) SendMessage(_ context.Context, chatID int64, text string) error {
	c.messages = append(c.messages, sendMessageCall{chatID: chatID, text: text})
	return nil
}

func TestInlineHandlerAnswersUsageForEmptyQuery(t *testing.T) {
	service := &fakeReelsService{}
	telegram := &fakeTelegramClient{}
	handler := InlineHandler{
		Service:  service,
		Telegram: telegram,
	}

	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-1"}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(service.calls) != 0 {
		t.Fatalf("service called for empty query: %v", service.calls)
	}
	if len(telegram.videoUploads) != 0 || len(telegram.photoUploads) != 0 {
		t.Fatalf("uploaded for empty query: videos=%v photos=%v", telegram.videoUploads, telegram.photoUploads)
	}
	assertUsageAnswer(t, telegram, "inline-1")
}

func TestInlineHandlerAnswersUsageForBadQuery(t *testing.T) {
	service := &fakeReelsService{}
	telegram := &fakeTelegramClient{}
	handler := InlineHandler{
		Service:  service,
		Telegram: telegram,
	}

	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-2", Query: "not-a-url"}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(service.calls) != 0 {
		t.Fatalf("service called for bad query: %v", service.calls)
	}
	assertUsageAnswer(t, telegram, "inline-2")
}

func TestInlineHandlerAnswersUsageForUnsupportedURL(t *testing.T) {
	service := &fakeReelsService{}
	telegram := &fakeTelegramClient{}
	handler := InlineHandler{
		Service:  service,
		Telegram: telegram,
	}

	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-unsupported", Query: "https://example.com/video.mp4"}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(service.calls) != 0 {
		t.Fatalf("service called for unsupported URL: %v", service.calls)
	}
	assertUsageAnswer(t, telegram, "inline-unsupported")
}

func TestInlineHandlerAnswersPreparingOnCacheMissAndStoresCachedVideo(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{{
			Kind:     MediaKindVideo,
			Filename: "reel.mp4",
			Bytes:    []byte("video bytes"),
		}},
	}
	telegram := &fakeTelegramClient{videoFileID: "telegram-file-id"}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         NewMediaCache(defaultMediaCacheTTL),
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/reel/abc123/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-3", Query: " " + reelURL + " "}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(service.calls) != 0 {
		t.Fatalf("service called before background task ran: %v", service.calls)
	}
	if len(telegram.videoUploads) != 0 || len(telegram.photoUploads) != 0 {
		t.Fatalf("uploaded before background task ran: videos=%v photos=%v", telegram.videoUploads, telegram.photoUploads)
	}
	assertPreparingAnswer(t, telegram, "inline-3")

	runner.runNext(t)

	if got, want := service.calls, []string{reelURL}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("service calls = %v, want %v", got, want)
	}
	if len(telegram.videoUploads) != 1 {
		t.Fatalf("video uploads = %d, want 1", len(telegram.videoUploads))
	}
	if len(telegram.photoUploads) != 0 {
		t.Fatalf("photo uploads = %d, want 0", len(telegram.photoUploads))
	}
	if telegram.videoUploads[0].chatID != -100123 {
		t.Fatalf("upload chatID = %d, want -100123", telegram.videoUploads[0].chatID)
	}
	if string(telegram.videoUploads[0].media.Bytes) != "video bytes" {
		t.Fatalf("uploaded bytes = %q", telegram.videoUploads[0].media.Bytes)
	}
	if telegram.videoUploads[0].media.Caption != sourceCaption(reelURL) {
		t.Fatalf("upload caption = %q, want source URL caption", telegram.videoUploads[0].media.Caption)
	}

	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-4", Query: reelURL}); err != nil {
		t.Fatalf("Handle(cached) error = %v", err)
	}
	if len(telegram.answerResults) != 1 {
		t.Fatalf("answer results = %d, want 1", len(telegram.answerResults))
	}
	result, ok := telegram.answerResults[0].(gotgbot.InlineQueryResultCachedVideo)
	if !ok {
		t.Fatalf("answer result type = %T, want InlineQueryResultCachedVideo", telegram.answerResults[0])
	}
	if result.VideoFileId != "telegram-file-id" {
		t.Fatalf("cached video file ID = %q, want telegram-file-id", result.VideoFileId)
	}
	if result.Caption != "" {
		t.Fatalf("cached video caption = %q, want no inline caption", result.Caption)
	}
	if result.Id == "" || len(result.Id) > 64 {
		t.Fatalf("result ID = %q, want non-empty and <= 64 bytes", result.Id)
	}
}

func TestInlineHandlerBackgroundJobUploadsMultipleMediaAndAnswersOnlyCachedVideos(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{
			{
				Kind:     MediaKindVideo,
				Filename: "reel.mp4",
				Bytes:    []byte("video bytes"),
			},
			{
				Kind:     MediaKindPhoto,
				Filename: "cover.jpg",
				Bytes:    []byte("photo bytes"),
			},
		},
	}
	telegram := &fakeTelegramClient{
		videoFileID: "telegram-video-id",
		photoFileID: "telegram-photo-id",
	}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         NewMediaCache(defaultMediaCacheTTL),
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/reel/abc123/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-media", Query: reelURL}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	assertPreparingAnswer(t, telegram, "inline-media")
	runner.runNext(t)
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-media-cached", Query: reelURL}); err != nil {
		t.Fatalf("Handle(cached) error = %v", err)
	}

	if len(telegram.videoUploads) != 1 {
		t.Fatalf("video uploads = %d, want 1", len(telegram.videoUploads))
	}
	if len(telegram.photoUploads) != 1 {
		t.Fatalf("photo uploads = %d, want 1", len(telegram.photoUploads))
	}
	if string(telegram.photoUploads[0].media.Bytes) != "photo bytes" {
		t.Fatalf("uploaded photo bytes = %q", telegram.photoUploads[0].media.Bytes)
	}
	if len(telegram.answerResults) != 1 {
		t.Fatalf("answer results = %d, want 1", len(telegram.answerResults))
	}
	videoResult, ok := telegram.answerResults[0].(gotgbot.InlineQueryResultCachedVideo)
	if !ok {
		t.Fatalf("answer result[0] type = %T, want InlineQueryResultCachedVideo", telegram.answerResults[0])
	}
	if videoResult.VideoFileId != "telegram-video-id" {
		t.Fatalf("cached video file ID = %q, want telegram-video-id", videoResult.VideoFileId)
	}
}

func TestInlineHandlerUploadsPlainPhotoButDoesNotCacheIt(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{{
			Kind:     MediaKindPhoto,
			Filename: "photo.jpg",
			Bytes:    []byte("original photo bytes"),
		}},
	}
	telegram := &fakeTelegramClient{photoFileID: "telegram-photo-id"}
	runner := &capturedRunner{}
	cache := NewMediaCache(defaultMediaCacheTTL)
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         cache,
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/p/plainphoto/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-photo", Query: reelURL}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	runner.runNext(t)

	if len(telegram.photoUploads) != 1 {
		t.Fatalf("photo uploads = %d, want 1", len(telegram.photoUploads))
	}
	if string(telegram.photoUploads[0].media.Bytes) != "original photo bytes" {
		t.Fatalf("uploaded photo bytes = %q", telegram.photoUploads[0].media.Bytes)
	}
	if len(telegram.videoUploads) != 0 {
		t.Fatalf("video uploads = %d, want 0", len(telegram.videoUploads))
	}
	if stats := cache.Stats(); stats.Entries != 0 {
		t.Fatalf("cache entries = %d, want 0 for photo-only result", stats.Entries)
	}

	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-photo-again", Query: reelURL}); err != nil {
		t.Fatalf("Handle(second) error = %v", err)
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("background tasks after repeated photo query = %d, want 1", len(runner.tasks))
	}
}

func TestInlineHandlerRejectsEmptyMediaItemBeforeUpload(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{{
			Kind:     MediaKindVideo,
			Filename: "reel.mp4",
		}},
	}
	telegram := &fakeTelegramClient{videoFileID: "telegram-file-id"}
	logger := &fakeLogger{}
	metrics := &fakeMetricsRecorder{}
	runner := &capturedRunner{}
	cache := NewMediaCache(defaultMediaCacheTTL)
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Logger:        logger,
		Cache:         cache,
		StorageChatID: -100123,
		RunBackground: runner.run,
		Metrics:       metrics,
	}

	const reelURL = "https://www.instagram.com/reel/empty/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-empty", Query: reelURL}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if err := handler.HandleChosen(context.Background(), ChosenInlineResult{
		ResultID:        "preparing",
		Query:           reelURL,
		InlineMessageID: "inline-message-empty",
	}); err != nil {
		t.Fatalf("HandleChosen() error = %v", err)
	}
	runner.runNext(t)

	if len(telegram.videoUploads) != 0 || len(telegram.photoUploads) != 0 {
		t.Fatalf("uploads = videos:%v photos:%v, want none", telegram.videoUploads, telegram.photoUploads)
	}
	if stats := cache.Stats(); stats.Entries != 0 {
		t.Fatalf("cache entries = %d, want 0", stats.Entries)
	}
	if len(telegram.textEdits) != 1 || telegram.textEdits[0].text != "Could not download this media." {
		t.Fatalf("text edits = %#v, want download failure edit", telegram.textEdits)
	}
	if got, want := metrics.downloads, []downloadMetric{{platform: "instagram", status: "empty_media"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("download metrics = %#v, want %#v", got, want)
	}
	if len(metrics.downloadSizes) != 0 || len(metrics.uploadSizes) != 0 || len(metrics.uploads) != 0 {
		t.Fatalf("metrics recorded for empty media: downloadSizes=%#v uploadSizes=%#v uploads=%#v", metrics.downloadSizes, metrics.uploadSizes, metrics.uploads)
	}
	if len(logger.errorMessages) != 1 || logger.errorMessages[0] != "download inline media returned empty item" {
		t.Fatalf("logger messages = %v", logger.errorMessages)
	}
	if !fieldsContain(logger.errorFields[0], "kind", MediaKindVideo) || !fieldsContain(logger.errorFields[0], "bytes", 0) {
		t.Fatalf("logger fields = %#v", logger.errorFields[0])
	}
}

func TestInlineHandlerUsesCachedMediaForRepeatedURL(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{{
			Kind:     MediaKindVideo,
			Filename: "reel.mp4",
			Bytes:    []byte("video bytes"),
		}},
	}
	telegram := &fakeTelegramClient{videoFileID: "telegram-file-id"}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         NewMediaCache(defaultMediaCacheTTL),
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/reel/abc123/?igsh=first"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-1", Query: reelURL}); err != nil {
		t.Fatalf("Handle(first) error = %v", err)
	}
	runner.runNext(t)
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-2", Query: " https://www.instagram.com/reel/abc123/?igsh=second "}); err != nil {
		t.Fatalf("Handle(second) error = %v", err)
	}

	if len(service.calls) != 1 {
		t.Fatalf("service calls = %v, want one download", service.calls)
	}
	if len(telegram.videoUploads) != 1 {
		t.Fatalf("video uploads = %d, want one upload", len(telegram.videoUploads))
	}
	result, ok := telegram.answerResults[0].(gotgbot.InlineQueryResultCachedVideo)
	if !ok {
		t.Fatalf("answer result type = %T, want InlineQueryResultCachedVideo", telegram.answerResults[0])
	}
	if result.VideoFileId != "telegram-file-id" {
		t.Fatalf("cached video file ID = %q, want telegram-file-id", result.VideoFileId)
	}
}

func TestInlineHandlerDedupesInFlightPrepareForRepeatedURL(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{{
			Kind:  MediaKindVideo,
			Bytes: []byte("video bytes"),
		}},
	}
	telegram := &fakeTelegramClient{videoFileID: "telegram-file-id"}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         NewMediaCache(defaultMediaCacheTTL),
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/reel/abc123/?igsh=first"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-1", Query: reelURL}); err != nil {
		t.Fatalf("Handle(first) error = %v", err)
	}
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-2", Query: "https://www.instagram.com/reel/abc123/?igsh=second"}); err != nil {
		t.Fatalf("Handle(second) error = %v", err)
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("background tasks = %d, want 1", len(runner.tasks))
	}
	if len(service.calls) != 0 {
		t.Fatalf("service called before background task ran: %v", service.calls)
	}
	assertPreparingAnswer(t, telegram, "inline-2")

	runner.runNext(t)
	if len(service.calls) != 1 {
		t.Fatalf("service calls = %v, want one download", service.calls)
	}
	if len(telegram.videoUploads) != 1 {
		t.Fatalf("video uploads = %d, want one upload", len(telegram.videoUploads))
	}
}

func TestInlineHandlerEditsChosenPlaceholderAfterPrepare(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{{
			Kind:     MediaKindVideo,
			Bytes:    []byte("video bytes"),
			Duration: 30,
			Width:    720,
			Height:   1280,
		}},
	}
	telegram := &fakeTelegramClient{videoFileID: "telegram-file-id"}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         NewMediaCache(defaultMediaCacheTTL),
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/p/DXg3XpNjG4g/?igsh=first"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-1", Query: reelURL}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	assertPreparingAnswer(t, telegram, "inline-1")
	if err := handler.HandleChosen(context.Background(), ChosenInlineResult{
		ResultID:        "preparing",
		Query:           reelURL,
		InlineMessageID: "inline-message-1",
	}); err != nil {
		t.Fatalf("HandleChosen() error = %v", err)
	}
	runner.runNext(t)

	if len(telegram.mediaEdits) != 1 {
		t.Fatalf("media edits = %d, want 1", len(telegram.mediaEdits))
	}
	edit := telegram.mediaEdits[0]
	if edit.inlineMessageID != "inline-message-1" {
		t.Fatalf("inline message ID = %q, want inline-message-1", edit.inlineMessageID)
	}
	if edit.media.FileID != "telegram-file-id" || edit.media.Kind != MediaKindVideo {
		t.Fatalf("edit media = %#v, want cached video file_id", edit.media)
	}
	if edit.media.Duration != 30 || edit.media.Width != 720 || edit.media.Height != 1280 {
		t.Fatalf("edit media dimensions = duration:%d width:%d height:%d", edit.media.Duration, edit.media.Width, edit.media.Height)
	}
}

func TestInlineHandlerRecordsMetricsForPrepareUploadAndSend(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{{
			Kind:  MediaKindVideo,
			Bytes: []byte("video bytes"),
		}},
	}
	telegram := &fakeTelegramClient{videoFileID: "telegram-file-id"}
	runner := &capturedRunner{}
	metrics := &fakeMetricsRecorder{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         NewMediaCache(defaultMediaCacheTTL),
		StorageChatID: -100123,
		RunBackground: runner.run,
		Metrics:       metrics,
	}

	const reelURL = "https://www.instagram.com/p/DXg3XpNjG4g/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-1", Query: reelURL}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if err := handler.HandleChosen(context.Background(), ChosenInlineResult{
		ResultID:        "preparing",
		Query:           reelURL,
		InlineMessageID: "inline-message-1",
	}); err != nil {
		t.Fatalf("HandleChosen() error = %v", err)
	}
	runner.runNext(t)

	if got, want := metrics.inlineQueries, []inlineQueryMetric{{result: "cache_miss_queued", platform: "instagram"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("inline query metrics = %#v, want %#v", got, want)
	}
	if got, want := metrics.inlineChosen, []inlineChosenMetric{{result: "waiting", kind: "unknown", source: "unknown", platform: "instagram"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("inline chosen metrics = %#v, want %#v", got, want)
	}
	if got, want := metrics.downloads, []downloadMetric{{platform: "instagram", status: "success"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("download metrics = %#v, want %#v", got, want)
	}
	if got, want := metrics.downloaded, []mediaMetric{{platform: "instagram", kind: "video"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("downloaded media metrics = %#v, want %#v", got, want)
	}
	if got, want := metrics.downloadSizes, []mediaMetric{{platform: "instagram", kind: "video", size: len("video bytes")}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("download size metrics = %#v, want %#v", got, want)
	}
	if got, want := metrics.uploadSizes, []mediaMetric{{kind: "video", size: len("video bytes")}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("upload size metrics = %#v, want %#v", got, want)
	}
	if got, want := metrics.uploads, []uploadMetric{{kind: "video", status: "success"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("upload metrics = %#v, want %#v", got, want)
	}
	if got, want := metrics.sent, []sentMetric{{kind: "video", source: "async", status: "success"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("sent metrics = %#v, want %#v", got, want)
	}
}

func TestInlineHandlerEditsChosenPlaceholderFromCache(t *testing.T) {
	service := &fakeReelsService{}
	telegram := &fakeTelegramClient{}
	cache := NewMediaCache(defaultMediaCacheTTL)
	const reelURL = "https://www.instagram.com/reel/abc123/"
	cache.Set(mediaCacheKey(reelURL), []CachedMedia{{
		Kind:   MediaKindVideo,
		FileID: "cached-file-id",
	}})
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         cache,
		StorageChatID: -100123,
	}

	if err := handler.HandleChosen(context.Background(), ChosenInlineResult{
		ResultID:        "preparing",
		Query:           reelURL,
		InlineMessageID: "inline-message-2",
	}); err != nil {
		t.Fatalf("HandleChosen() error = %v", err)
	}

	if len(service.calls) != 0 {
		t.Fatalf("service calls = %v, want none", service.calls)
	}
	if len(telegram.mediaEdits) != 1 {
		t.Fatalf("media edits = %d, want 1", len(telegram.mediaEdits))
	}
	if telegram.mediaEdits[0].media.FileID != "cached-file-id" {
		t.Fatalf("edited file ID = %q, want cached-file-id", telegram.mediaEdits[0].media.FileID)
	}
}

func TestInlineHandlerEditsChosenPlaceholderOnPrepareFailure(t *testing.T) {
	service := &fakeReelsService{err: errors.New("download failed")}
	telegram := &fakeTelegramClient{}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         NewMediaCache(defaultMediaCacheTTL),
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/reel/abc123/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-1", Query: reelURL}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if err := handler.HandleChosen(context.Background(), ChosenInlineResult{
		ResultID:        "preparing",
		Query:           reelURL,
		InlineMessageID: "inline-message-3",
	}); err != nil {
		t.Fatalf("HandleChosen() error = %v", err)
	}
	runner.runNext(t)

	if len(telegram.textEdits) != 1 {
		t.Fatalf("text edits = %d, want 1", len(telegram.textEdits))
	}
	if telegram.textEdits[0].inlineMessageID != "inline-message-3" {
		t.Fatalf("inline message ID = %q, want inline-message-3", telegram.textEdits[0].inlineMessageID)
	}
	if telegram.textEdits[0].text != "Could not download this media." {
		t.Fatalf("text edit = %q", telegram.textEdits[0].text)
	}
}

func TestInlineHandlerExpiresCachedMediaAfterTTL(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cache := newMediaCacheWithClock(defaultMediaCacheTTL, func() time.Time { return now })
	service := &fakeReelsService{
		media: []Media{{
			Kind:  MediaKindVideo,
			Bytes: []byte("video bytes"),
		}},
	}
	telegram := &fakeTelegramClient{videoFileID: "telegram-file-id"}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         cache,
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/reel/abc123/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-1", Query: reelURL}); err != nil {
		t.Fatalf("Handle(first) error = %v", err)
	}
	runner.runNext(t)
	now = now.Add(defaultMediaCacheTTL + time.Nanosecond)
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-2", Query: reelURL}); err != nil {
		t.Fatalf("Handle(second) error = %v", err)
	}
	runner.runNext(t)

	if len(service.calls) != 2 {
		t.Fatalf("service calls = %v, want two downloads after expiry", service.calls)
	}
	if len(telegram.videoUploads) != 2 {
		t.Fatalf("video uploads = %d, want two uploads after expiry", len(telegram.videoUploads))
	}
}

func TestInlineHandlerDoesNotCacheDownloadFailure(t *testing.T) {
	service := &fakeReelsService{err: errors.New("download failed")}
	telegram := &fakeTelegramClient{}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Cache:         NewMediaCache(defaultMediaCacheTTL),
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	const reelURL = "https://www.instagram.com/reel/abc123/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-1", Query: reelURL}); err != nil {
		t.Fatalf("Handle(first) error = %v", err)
	}
	runner.runNext(t)
	service.err = nil
	service.media = []Media{{
		Kind:  MediaKindVideo,
		Bytes: []byte("video bytes"),
	}}
	telegram.videoFileID = "telegram-file-id"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-2", Query: reelURL}); err != nil {
		t.Fatalf("Handle(second) error = %v", err)
	}
	runner.runNext(t)

	if len(service.calls) != 2 {
		t.Fatalf("service calls = %v, want retry after failed download", service.calls)
	}
	if len(telegram.videoUploads) != 1 {
		t.Fatalf("video uploads = %d, want upload after retry", len(telegram.videoUploads))
	}
}

func TestInlineHandlerLogsBackgroundDownloadError(t *testing.T) {
	service := &fakeReelsService{err: errors.New("download failed")}
	telegram := &fakeTelegramClient{}
	logger := &fakeLogger{}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:       service,
		Telegram:      telegram,
		Logger:        logger,
		StorageChatID: -100123,
		RunBackground: runner.run,
	}

	err := handler.Handle(context.Background(), InlineQuery{ID: "inline-4", Query: "https://www.instagram.com/reel/abc123/"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	assertPreparingAnswer(t, telegram, "inline-4")
	runner.runNext(t)
	if len(telegram.videoUploads) != 0 || len(telegram.photoUploads) != 0 {
		t.Fatalf("uploaded after download failure: videos=%v photos=%v", telegram.videoUploads, telegram.photoUploads)
	}
	if len(logger.errorMessages) != 1 || logger.errorMessages[0] != "download inline media failed" {
		t.Fatalf("logger messages = %v", logger.errorMessages)
	}
	if !fieldsContain(logger.errorFields[0], "url_host", "www.instagram.com") || !fieldsContain(logger.errorFields[0], "url_path", "/reel/abc123/") {
		t.Fatalf("logger fields = %#v", logger.errorFields[0])
	}
}

func TestInlineHandlerRegistersUserStorageOnStart(t *testing.T) {
	registry, err := LoadUserStorageRegistry(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatalf("LoadUserStorageRegistry() error = %v", err)
	}
	telegram := &fakeTelegramClient{}
	metrics := &fakeMetricsRecorder{}
	handler := InlineHandler{
		Telegram:        telegram,
		StorageRegistry: registry,
		Metrics:         metrics,
	}

	if err := handler.HandleStart(context.Background(), StartCommand{UserID: 777, ChatID: 348313485}); err != nil {
		t.Fatalf("HandleStart() error = %v", err)
	}

	storage, ok := registry.Get(777)
	if !ok {
		t.Fatal("registered storage not found")
	}
	if storage.ChatID != 348313485 {
		t.Fatalf("storage chat id = %d, want 348313485", storage.ChatID)
	}
	if len(telegram.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(telegram.messages))
	}
	if telegram.messages[0].chatID != 348313485 {
		t.Fatalf("message chat id = %d, want 348313485", telegram.messages[0].chatID)
	}
	if registry.Count() != 1 {
		t.Fatalf("registry count = %d, want 1", registry.Count())
	}
	if got, want := metrics.startResults, []string{"success"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("start metrics = %#v, want %#v", got, want)
	}
}

func TestInlineHandlerDoesNotRegisterGroupStart(t *testing.T) {
	registry, err := LoadUserStorageRegistry(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatalf("LoadUserStorageRegistry() error = %v", err)
	}
	telegram := &fakeTelegramClient{}
	handler := InlineHandler{
		Telegram:        telegram,
		StorageRegistry: registry,
	}

	if err := handler.HandleStart(context.Background(), StartCommand{UserID: 777, ChatID: -100123, ChatType: "group"}); err != nil {
		t.Fatalf("HandleStart() error = %v", err)
	}

	if _, ok := registry.Get(777); ok {
		t.Fatal("group /start should not register user storage")
	}
	if len(telegram.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(telegram.messages))
	}
}

func TestInlineHandlerRequiresStartWhenRegistryHasNoUser(t *testing.T) {
	service := &fakeReelsService{}
	telegram := &fakeTelegramClient{}
	registry, err := LoadUserStorageRegistry(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatalf("LoadUserStorageRegistry() error = %v", err)
	}
	handler := InlineHandler{
		Service:         service,
		Telegram:        telegram,
		StorageRegistry: registry,
	}

	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-start", Query: "https://www.instagram.com/reel/abc123/", FromUserID: 777}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(service.calls) != 0 {
		t.Fatalf("service calls = %v, want none", service.calls)
	}
	assertArticleAnswer(t, telegram, "inline-start")
	result := telegram.answerResults[0].(gotgbot.InlineQueryResultArticle)
	if result.Id != "start-required" {
		t.Fatalf("article ID = %q, want start-required", result.Id)
	}
}

func TestInlineHandlerUsesRegisteredUserStorageChat(t *testing.T) {
	service := &fakeReelsService{
		media: []Media{{
			Kind:  MediaKindVideo,
			Bytes: []byte("video bytes"),
		}},
	}
	telegram := &fakeTelegramClient{videoFileID: "telegram-file-id"}
	registry, err := LoadUserStorageRegistry(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatalf("LoadUserStorageRegistry() error = %v", err)
	}
	if err := registry.Register(777, 348313485); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	runner := &capturedRunner{}
	handler := InlineHandler{
		Service:         service,
		Telegram:        telegram,
		Cache:           NewMediaCache(defaultMediaCacheTTL),
		StorageRegistry: registry,
		RunBackground:   runner.run,
	}

	const reelURL = "https://www.instagram.com/reel/abc123/"
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-user", Query: reelURL, FromUserID: 777}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	runner.runNext(t)

	if len(telegram.videoUploads) != 1 {
		t.Fatalf("video uploads = %d, want 1", len(telegram.videoUploads))
	}
	if telegram.videoUploads[0].chatID != 348313485 {
		t.Fatalf("upload chat id = %d, want 348313485", telegram.videoUploads[0].chatID)
	}
	if err := handler.Handle(context.Background(), InlineQuery{ID: "inline-cached-user", Query: reelURL, FromUserID: 888}); err != nil {
		t.Fatalf("Handle(unregistered) error = %v", err)
	}
	result := telegram.answerResults[0].(gotgbot.InlineQueryResultArticle)
	if result.Id != "start-required" {
		t.Fatalf("unregistered user article ID = %q, want start-required", result.Id)
	}
}

func fieldsContain(fields []any, key string, value any) bool {
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == key && fields[i+1] == value {
			return true
		}
	}
	return false
}

func assertUsageAnswer(t *testing.T, telegram *fakeTelegramClient, queryID string) {
	t.Helper()
	assertArticleAnswer(t, telegram, queryID)
}

func assertArticleAnswer(t *testing.T, telegram *fakeTelegramClient, queryID string) {
	t.Helper()

	if telegram.answerQueryID != queryID {
		t.Fatalf("answer query ID = %q, want %q", telegram.answerQueryID, queryID)
	}
	if telegram.answerOpts == nil || !telegram.answerOpts.IsPersonal || telegram.answerOpts.CacheTime == nil || *telegram.answerOpts.CacheTime != 0 {
		t.Fatalf("answer opts = %#v, want personal cache_time=0", telegram.answerOpts)
	}
	if len(telegram.answerResults) != 1 {
		t.Fatalf("answer results = %d, want 1", len(telegram.answerResults))
	}
	if _, ok := telegram.answerResults[0].(gotgbot.InlineQueryResultArticle); !ok {
		t.Fatalf("answer result type = %T, want InlineQueryResultArticle", telegram.answerResults[0])
	}
}

func assertPreparingAnswer(t *testing.T, telegram *fakeTelegramClient, queryID string) {
	t.Helper()
	assertArticleAnswer(t, telegram, queryID)
	result := telegram.answerResults[0].(gotgbot.InlineQueryResultArticle)
	if result.Id != "preparing" {
		t.Fatalf("article ID = %q, want preparing", result.Id)
	}
	if result.ReplyMarkup == nil || len(result.ReplyMarkup.InlineKeyboard) == 0 {
		t.Fatalf("preparing article has no inline keyboard")
	}
}
