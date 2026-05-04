package reels

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultOutputFilename  = "reel.mp4"
	defaultOutputMIME      = "video/mp4"
	defaultPhotoFilename   = "photo.jpg"
	defaultPhotoMIME       = "image/jpeg"
	photoAudioMaxDuration  = 90 * time.Second
	photoSlideDuration     = 4 * time.Second
	photoSlideTransition   = 500 * time.Millisecond
	photoVideoMaxDimension = 480
	photoVideoFrameRate    = 6
	singlePhotoFrameRate   = 1
	audioLoopSampleRate    = 44100
	maxStderrBytes         = 4096
	instagramUserAgent     = "Mozilla/5.0"
	instagramAPIBaseURL    = "https://www.instagram.com"
	tikTokAPIBaseURL       = "https://www.tikwm.com"
	tikTokMobileAPIBaseURL = "https://api16-normal-c-useast1a.tiktokv.com"
)

var (
	ErrDownloadTooLarge           = errors.New("download exceeds max bytes")
	ErrInvalidMaxBytes            = errors.New("max bytes must be positive")
	ErrInstagramAudioUnavailable  = errors.New("instagram audio is unavailable")
	ErrInstagramCookiesRequired   = errors.New("instagram cookies are required to fetch audio metadata")
	instagramShortcodeAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	instagramDirectAudioFieldKeys = map[string]struct{}{
		"audio_url":                {},
		"progressive_download_url": {},
		"dash_manifest":            {},
	}
	instagramMusicMarkerKeys = map[string]struct{}{
		"audio_asset_id":                     {},
		"audio_cluster_id":                   {},
		"clips_metadata":                     {},
		"music_canonical_id":                 {},
		"music_info":                         {},
		"music_metadata":                     {},
		"original_sound_info":                {},
		"original_audio_title":               {},
		"product_type":                       {},
		"uses_original_audio":                {},
		"clips_music_attribution_info":       {},
		"ig_artist":                          {},
		"song_name":                          {},
		"dash_manifest_raw":                  {},
		"should_mute_audio_reason":           {},
		"is_artist_pick":                     {},
		"audio_ranking_info":                 {},
		"audio_type":                         {},
		"music_consumption_info":             {},
		"featured_label":                     {},
		"music_canonical_id_for_consumption": {},
	}
	instagramImageFieldKeys = map[string]struct{}{
		"display_url":   {},
		"thumbnail_src": {},
		"url":           {},
	}
)

type Downloader struct {
	YTDLPPath              string
	FFmpegPath             string
	InstagramCookiesFile   string
	InstagramAPIBaseURL    string
	Timeout                time.Duration
	MaxBytes               int64
	TikTokAPIBaseURL       string
	TikTokMobileAPIBaseURL string
	Metrics                FFmpegMetricsRecorder
}

type FFmpegMetricsRecorder interface {
	ObserveFFmpeg(platform string, operation string, status string, duration time.Duration)
}

type MediaKind string

const (
	MediaKindVideo MediaKind = "video"
	MediaKindPhoto MediaKind = "photo"
)

type DownloadedMedia struct {
	Kind     MediaKind
	Bytes    []byte
	Filename string
	MIME     string
	Title    string
	Duration float64
	Width    int
	Height   int
}

type instagramAudioSelection struct {
	URL            string
	StartOffset    time.Duration
	Duration       time.Duration
	HasStartOffset bool
	HasDuration    bool
}

func (s instagramAudioSelection) effectiveDuration() time.Duration {
	duration := photoAudioMaxDuration
	if s.HasDuration && s.Duration > 0 {
		duration = minDuration(s.Duration, photoAudioMaxDuration)
	}
	if duration <= 0 {
		return photoAudioMaxDuration
	}
	return duration
}

func (d Downloader) Download(ctx context.Context, rawURL string) ([]DownloadedMedia, error) {
	if d.MaxBytes <= 0 {
		return nil, ErrInvalidMaxBytes
	}

	ctx, cancel := d.context(ctx)
	defer cancel()

	metadata, err := d.fetchMetadata(ctx, rawURL)
	if err != nil {
		if isInstagramPhotoPostFallback(rawURL, err) {
			return d.downloadInstagramPhotoPost(ctx, rawURL)
		}
		if isTikTokPhotoPostFallback(rawURL, err) {
			return d.downloadTikTokPhotoPost(ctx, rawURL)
		}
		return nil, err
	}

	body, err := d.downloadBody(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	return []DownloadedMedia{{
		Kind:     MediaKindVideo,
		Bytes:    body,
		Filename: defaultOutputFilename,
		MIME:     defaultOutputMIME,
		Title:    metadata.Title,
		Duration: metadata.Duration,
		Width:    metadata.Width,
		Height:   metadata.Height,
	}}, nil
}

func (d Downloader) context(parent context.Context) (context.Context, context.CancelFunc) {
	if d.Timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, d.Timeout)
}

func (d Downloader) fetchMetadata(ctx context.Context, rawURL string) (ytDLPMetadata, error) {
	var stderr cappedBuffer
	cmd := exec.CommandContext(ctx, d.executable(), d.metadataArgs(rawURL)...)
	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		return ytDLPMetadata{}, commandError("yt-dlp metadata", err, stderr.String(), ctx.Err())
	}

	var metadata ytDLPMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return ytDLPMetadata{}, fmt.Errorf("parse yt-dlp metadata: %w", err)
	}

	return metadata, nil
}

func (d Downloader) downloadBody(ctx context.Context, rawURL string) ([]byte, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(childCtx, d.executable(), d.downloadArgs(rawURL)...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open yt-dlp stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("open yt-dlp stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start yt-dlp download: %w", err)
	}

	var stderrBuf cappedBuffer
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stderrBuf, stderr)
		close(stderrDone)
	}()

	body, readErr := readBounded(stdout, d.MaxBytes)
	if errors.Is(readErr, ErrDownloadTooLarge) {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	waitErr := cmd.Wait()
	<-stderrDone

	if readErr != nil {
		if errors.Is(readErr, ErrDownloadTooLarge) {
			return nil, fmt.Errorf("%w: limit=%d stderr=%q", ErrDownloadTooLarge, d.MaxBytes, stderrBuf.String())
		}
		return nil, fmt.Errorf("read yt-dlp stdout: %w", readErr)
	}
	if waitErr != nil {
		return nil, commandError("yt-dlp download", waitErr, stderrBuf.String(), ctx.Err())
	}

	return body, nil
}

func (d Downloader) executable() string {
	if d.YTDLPPath == "" {
		return "yt-dlp"
	}
	return d.YTDLPPath
}

func (d Downloader) ffmpegExecutable() string {
	if d.FFmpegPath == "" {
		return "ffmpeg"
	}
	return d.FFmpegPath
}

func (d Downloader) metadataArgs(rawURL string) []string {
	args := append(baseArgs(), "-j")
	args = append(args, formatArgs()...)
	args = d.withInstagramCookies(args, rawURL)
	return append(args, rawURL)
}

func (d Downloader) downloadArgs(rawURL string) []string {
	args := append(baseArgs(), "--max-filesize", strconv.FormatInt(d.MaxBytes, 10))
	args = append(args, formatArgs()...)
	args = append(args, "--output", "-")
	args = d.withInstagramCookies(args, rawURL)
	return append(args, rawURL)
}

func baseArgs() []string {
	return []string{
		"--ignore-config",
		"--no-playlist",
		"--no-warnings",
		"--no-write-comments",
		"--no-cache-dir",
		"--socket-timeout", "15",
		"--retries", "2",
		"--fragment-retries", "2",
	}
}

func formatArgs() []string {
	return []string{
		"--format", "b[ext=mp4][vcodec!=none][acodec!=none][filesize<48M]/b[ext=mp4][vcodec!=none][acodec!=none][filesize_approx<48M]/b[ext=mp4][vcodec!=none][acodec!=none]/1/b[ext=mp4]/best[ext=mp4]/best",
		"--format-sort", "size:48M,res:720,+codec:avc:m4a",
	}
}

func (d Downloader) withInstagramCookies(args []string, rawURL string) []string {
	if d.InstagramCookiesFile == "" || !isInstagramDownloadURL(rawURL) {
		return args
	}
	return append(args, "--cookies", d.InstagramCookiesFile)
}

func readBounded(src io.Reader, maxBytes int64) ([]byte, error) {
	var dst bytes.Buffer
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			remaining := maxBytes + 1 - int64(dst.Len())
			if remaining <= 0 {
				return nil, ErrDownloadTooLarge
			}
			if int64(n) > remaining {
				dst.Write(buf[:remaining])
				return nil, ErrDownloadTooLarge
			}
			dst.Write(buf[:n])
			if int64(dst.Len()) > maxBytes {
				return nil, ErrDownloadTooLarge
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return dst.Bytes(), nil
			}
			return nil, err
		}
	}
}

func commandError(op string, err error, stderr string, ctxErr error) error {
	if ctxErr != nil {
		return fmt.Errorf("%s: %w: stderr=%q", op, ctxErr, stderr)
	}
	return fmt.Errorf("%s: %w: stderr=%q", op, err, stderr)
}

func isInstagramPhotoPostFallback(rawURL string, err error) bool {
	if !isInstagramPostURL(rawURL) && !strings.HasPrefix(urlPath(rawURL, ""), "/p/") {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "there is no video in this post") ||
		strings.Contains(message, "no video formats found")
}

func isTikTokPhotoPostFallback(rawURL string, err error) bool {
	if !isTikTokDownloadURL(rawURL) {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unsupported url") && strings.Contains(message, "/photo/")
}

func isTikTokDownloadURL(rawURL string) bool {
	return tikTokURLPath(rawURL, "") != ""
}

func isInstagramDownloadURL(rawURL string) bool {
	return instagramURLPath(rawURL, "") != ""
}

func isInstagramPostURL(rawURL string) bool {
	return strings.HasPrefix(instagramURLPath(rawURL, ""), "/p/")
}

func instagramURLPath(rawURL string, fallback string) string {
	value := rawURL
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fallback
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "instagram.com" && host != "www.instagram.com" {
		return fallback
	}
	return parsed.EscapedPath()
}

func urlPath(rawURL string, fallback string) string {
	value := rawURL
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fallback
	}
	return parsed.EscapedPath()
}

func tikTokURLPath(rawURL string, fallback string) string {
	value := rawURL
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fallback
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "tiktok.com", "www.tiktok.com", "vm.tiktok.com", "vt.tiktok.com":
		return parsed.EscapedPath()
	default:
		return fallback
	}
}

type ytDLPMetadata struct {
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
}

type tikTokPhotoAPIResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data tikTokPhotoData `json:"data"`
}

type tikTokPhotoData struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	Cover            string          `json:"cover"`
	Origin           string          `json:"origin_cover"`
	Images           []string        `json:"images"`
	Music            string          `json:"music"`
	Play             string          `json:"play"`
	Duration         float64         `json:"duration"`
	MusicBeginTimeMS *float64        `json:"music_begin_time_in_ms"`
	MusicEndTimeMS   *float64        `json:"music_end_time_in_ms"`
	MusicInfo        tikTokMusicInfo `json:"music_info"`
}

type tikTokMusicInfo struct {
	Play     string  `json:"play"`
	Duration float64 `json:"duration"`
}

type tikTokPhotoAudioTiming struct {
	BeginMS  float64
	EndMS    float64
	HasBegin bool
	HasEnd   bool
}

type cappedBuffer struct {
	buf bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := maxStderrBytes - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

func (d Downloader) downloadInstagramPhotoPost(ctx context.Context, rawURL string) ([]DownloadedMedia, error) {
	page, err := d.fetchInstagramPage(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	images := uniqueStrings(extractInstagramImageURLs(page))
	audioSelection := instagramAudioSelection{URL: firstString(extractInstagramAudioURLs(page))}
	hasMusicMarker := hasInstagramMusicMarker(page)
	metadataFetched := false

	if d.InstagramAPIBaseURL != "" || isInstagramPostURL(rawURL) {
		if metadata, err := d.fetchInstagramMediaInfo(ctx, rawURL); err == nil {
			metadataFetched = true
			metadataImages := extractInstagramMetadataImageURLs(metadata)
			if len(metadataImages) > 0 {
				images = uniqueStrings(metadataImages)
			}
			audioSelection = mergeInstagramAudioSelection(audioSelection, extractInstagramMetadataAudioSelection(metadata))
			hasMusicMarker = hasMusicMarker || hasInstagramMetadataMusicMarker(metadata)
		} else if d.InstagramCookiesFile == "" && isInstagramPostURL(rawURL) {
			hasMusicMarker = true
		}
	}

	if len(images) == 0 {
		return nil, fmt.Errorf("instagram photo post fallback: no image urls found")
	}
	title := firstString(extractMetaContent(page, `og:title`))

	photos := make([]DownloadedMedia, 0, len(images))
	for i, imageURL := range images {
		body, mime, err := d.fetchBoundedURL(ctx, imageURL, rawURL)
		if err != nil {
			return nil, fmt.Errorf("download instagram photo %d: %w", i+1, err)
		}
		filename := defaultPhotoFilename
		if len(images) > 1 {
			filename = fmt.Sprintf("photo-%d.jpg", i+1)
		}
		if mime == "" {
			mime = defaultPhotoMIME
		}
		photos = append(photos, DownloadedMedia{
			Kind:     MediaKindPhoto,
			Bytes:    body,
			Filename: filename,
			MIME:     mime,
			Title:    title,
		})
	}

	if audioSelection.URL == "" {
		if hasMusicMarker {
			if d.InstagramCookiesFile == "" && !metadataFetched {
				return nil, fmt.Errorf("%w: set INSTAGRAM_COOKIES_FILE for Instagram photo posts with music", ErrInstagramCookiesRequired)
			}
			return nil, fmt.Errorf("%w: no direct audio URL found in Instagram metadata", ErrInstagramAudioUnavailable)
		}
		return photos, nil
	}

	audio, _, err := d.fetchBoundedURL(ctx, audioSelection.URL, rawURL)
	if err != nil {
		return nil, fmt.Errorf("download instagram audio: %w", err)
	}
	duration := audioSelection.effectiveDuration()
	videoImages := downloadedMediaBytes(photos)
	videoDuration := slideshowVideoDuration(duration, len(videoImages))
	video, err := d.synthesizeSlideshowVideo(ctx, videoImages, audio, audioSelection.StartOffset, duration, "instagram")
	if err != nil {
		return nil, err
	}
	width, height := slideshowCanvasSize(videoImages)

	return []DownloadedMedia{{
		Kind:     MediaKindVideo,
		Bytes:    video,
		Filename: defaultOutputFilename,
		MIME:     defaultOutputMIME,
		Title:    title,
		Duration: videoDuration.Seconds(),
		Width:    width,
		Height:   height,
	}}, nil
}

func (d Downloader) downloadTikTokPhotoPost(ctx context.Context, rawURL string) ([]DownloadedMedia, error) {
	metadata, err := d.fetchTikTokPhotoInfo(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	images := uniqueStrings(metadata.Data.Images)
	if len(images) == 0 {
		images = uniqueStrings([]string{metadata.Data.Origin, metadata.Data.Cover})
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("tiktok photo fallback: no image urls found")
	}

	title := metadata.Data.Title
	photos := make([]DownloadedMedia, 0, len(images))
	for i, imageURL := range images {
		body, mime, err := d.fetchBoundedURL(ctx, imageURL, rawURL)
		if err != nil {
			return nil, fmt.Errorf("download tiktok photo %d: %w", i+1, err)
		}
		filename := defaultPhotoFilename
		if len(images) > 1 {
			filename = fmt.Sprintf("photo-%d.jpg", i+1)
		}
		if mime == "" {
			mime = defaultPhotoMIME
		}
		photos = append(photos, DownloadedMedia{
			Kind:     MediaKindPhoto,
			Bytes:    body,
			Filename: filename,
			MIME:     mime,
			Title:    title,
		})
	}

	audioSelection := tikTokPhotoAudioSelection(metadata)
	if audioSelection.URL == "" {
		return photos, nil
	}
	if !audioSelection.HasStartOffset && d.shouldFetchTikTokAudioTimingFallback(rawURL) {
		if timing, err := d.fetchTikTokAwemeAudioTiming(ctx, tikTokAwemeID(rawURL, metadata)); err == nil {
			audioSelection = mergeTikTokPhotoAudioTiming(audioSelection, timing)
		} else if timing, err := d.fetchTikTokPageAudioTiming(ctx, rawURL); err == nil {
			audioSelection = mergeTikTokPhotoAudioTiming(audioSelection, timing)
		}
	}

	audio, _, err := d.fetchBoundedURL(ctx, audioSelection.URL, rawURL)
	if err != nil {
		return nil, fmt.Errorf("download tiktok audio: %w", err)
	}
	duration := audioSelection.effectiveDuration()
	videoImages := downloadedMediaBytes(photos)
	videoDuration := slideshowVideoDuration(duration, len(videoImages))
	video, err := d.synthesizeSlideshowVideo(ctx, videoImages, audio, audioSelection.StartOffset, duration, "tiktok")
	if err != nil {
		return nil, err
	}
	width, height := slideshowCanvasSize(videoImages)

	return []DownloadedMedia{{
		Kind:     MediaKindVideo,
		Bytes:    video,
		Filename: defaultOutputFilename,
		MIME:     defaultOutputMIME,
		Title:    title,
		Duration: videoDuration.Seconds(),
		Width:    width,
		Height:   height,
	}}, nil
}

func (d Downloader) fetchTikTokPhotoInfo(ctx context.Context, rawURL string) (tikTokPhotoAPIResponse, error) {
	baseURL := d.TikTokAPIBaseURL
	if baseURL == "" {
		baseURL = tikTokAPIBaseURL
	}
	endpoint, err := url.JoinPath(baseURL, "api")
	if err != nil {
		return tikTokPhotoAPIResponse{}, fmt.Errorf("tiktok photo info URL: %w", err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return tikTokPhotoAPIResponse{}, fmt.Errorf("parse tiktok photo info URL: %w", err)
	}
	query := parsed.Query()
	query.Set("url", rawURL)
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return tikTokPhotoAPIResponse{}, fmt.Errorf("create tiktok photo info request: %w", err)
	}
	req.Header.Set("User-Agent", instagramUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tikTokPhotoAPIResponse{}, fmt.Errorf("fetch tiktok photo info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tikTokPhotoAPIResponse{}, fmt.Errorf("fetch tiktok photo info: status %s", resp.Status)
	}

	body, err := readBounded(resp.Body, minPositive(d.MaxBytes, 5_000_000))
	if err != nil {
		return tikTokPhotoAPIResponse{}, fmt.Errorf("read tiktok photo info: %w", err)
	}

	var metadata tikTokPhotoAPIResponse
	if err := json.Unmarshal(body, &metadata); err != nil {
		return tikTokPhotoAPIResponse{}, fmt.Errorf("parse tiktok photo info: %w", err)
	}
	if metadata.Code != 0 {
		if metadata.Msg == "" {
			metadata.Msg = "unknown error"
		}
		return tikTokPhotoAPIResponse{}, fmt.Errorf("tiktok photo info: %s", metadata.Msg)
	}
	return metadata, nil
}

func (d Downloader) shouldFetchTikTokAudioTimingFallback(rawURL string) bool {
	if d.TikTokAPIBaseURL == "" {
		return true
	}
	if d.TikTokMobileAPIBaseURL != "" {
		return true
	}
	return tikTokURLPath(rawURL, "") == ""
}

func (d Downloader) fetchTikTokAwemeAudioTiming(ctx context.Context, awemeID string) (tikTokPhotoAudioTiming, error) {
	if awemeID == "" {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("tiktok aweme timing: aweme id not found")
	}
	baseURL := d.TikTokMobileAPIBaseURL
	if baseURL == "" {
		baseURL = tikTokMobileAPIBaseURL
	}
	endpoint, err := url.JoinPath(baseURL, "aweme", "v1", "feed")
	if err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("tiktok aweme timing URL: %w", err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("parse tiktok aweme timing URL: %w", err)
	}
	query := parsed.Query()
	for key, value := range tikTokMobileAPIQuery() {
		query.Set(key, value)
	}
	query.Set("aweme_id", awemeID)
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("create tiktok aweme timing request: %w", err)
	}
	req.Header.Set("User-Agent", tikTokMobileUserAgent())
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: "odin_tt", Value: randomHex(160)})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("fetch tiktok aweme timing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("fetch tiktok aweme timing: status %s", resp.Status)
	}

	body, err := readBounded(resp.Body, minPositive(d.MaxBytes, 10_000_000))
	if err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("read tiktok aweme timing: %w", err)
	}
	var metadata any
	if err := json.Unmarshal(body, &metadata); err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("parse tiktok aweme timing: %w", err)
	}
	timing := extractTikTokAwemeAudioTiming(metadata, awemeID)
	if !timing.valid() {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("tiktok aweme timing: audio offset not found")
	}
	return timing, nil
}

func (d Downloader) fetchTikTokPageAudioTiming(ctx context.Context, rawURL string) (tikTokPhotoAudioTiming, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("create tiktok page request: %w", err)
	}
	req.Header.Set("User-Agent", instagramUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("fetch tiktok page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("fetch tiktok page: status %s", resp.Status)
	}

	body, err := readBounded(resp.Body, minPositive(d.MaxBytes, 5_000_000))
	if err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("read tiktok page: %w", err)
	}
	return extractTikTokPageAudioTiming(string(body))
}

func extractTikTokPageAudioTiming(page string) (tikTokPhotoAudioTiming, error) {
	pattern := regexp.MustCompile(`(?is)<script\b[^>]*\bid=["']__UNIVERSAL_DATA_FOR_REHYDRATION__["'][^>]*>(.*?)</script>`)
	match := pattern.FindStringSubmatch(page)
	if len(match) != 2 {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("tiktok page timing: universal data not found")
	}
	var metadata any
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &metadata); err != nil {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("parse tiktok page timing: %w", err)
	}
	timing := extractTikTokAudioTiming(metadata)
	if !timing.valid() {
		return tikTokPhotoAudioTiming{}, fmt.Errorf("tiktok page timing: audio offset not found")
	}
	return timing, nil
}

func extractTikTokAwemeAudioTiming(metadata any, awemeID string) tikTokPhotoAudioTiming {
	root, ok := metadata.(map[string]any)
	if !ok {
		return tikTokPhotoAudioTiming{}
	}
	items, ok := root["aweme_list"].([]any)
	if !ok {
		return extractTikTokAudioTiming(metadata)
	}
	for _, item := range items {
		typed, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if itemID, ok := typed["aweme_id"].(string); ok && itemID != "" && itemID != awemeID {
			continue
		}
		timing := extractTikTokAudioTiming(typed)
		if timing.valid() {
			return timing
		}
	}
	return tikTokPhotoAudioTiming{}
}

func tikTokPhotoAudioSelection(metadata tikTokPhotoAPIResponse) instagramAudioSelection {
	selection := instagramAudioSelection{
		URL: firstString([]string{metadata.Data.MusicInfo.Play, metadata.Data.Music, metadata.Data.Play}),
	}
	selection = mergeTikTokPhotoAudioTiming(selection, tikTokPhotoAudioTimingFromMetadata(metadata))
	if !selection.HasDuration {
		selection.Duration = tikTokPhotoAudioDuration(metadata)
		selection.HasDuration = true
	}
	return selection
}

func tikTokAwemeID(rawURL string, metadata tikTokPhotoAPIResponse) string {
	if metadata.Data.ID != "" {
		return metadata.Data.ID
	}
	value := rawURL
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if isDigits(parts[i]) {
			return parts[i]
		}
	}
	return ""
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func tikTokMobileAPIQuery() map[string]string {
	now := time.Now()
	return map[string]string{
		"device_platform":       "android",
		"os":                    "android",
		"ssmix":                 "a",
		"_rticket":              strconv.FormatInt(now.UnixMilli(), 10),
		"cdid":                  randomUUID(),
		"channel":               "googleplay",
		"aid":                   "0",
		"app_name":              "musical_ly",
		"version_code":          "350103",
		"version_name":          "35.1.3",
		"manifest_version_code": "2023501030",
		"update_version_code":   "2023501030",
		"ab_version":            "35.1.3",
		"resolution":            "1080*2400",
		"dpi":                   "420",
		"device_type":           "Pixel 7",
		"device_brand":          "Google",
		"language":              "en",
		"os_api":                "29",
		"os_version":            "13",
		"ac":                    "wifi",
		"is_pad":                "0",
		"current_region":        "US",
		"app_type":              "normal",
		"sys_region":            "US",
		"last_install_time":     strconv.FormatInt(now.Add(-100_000*time.Second).Unix(), 10),
		"timezone_name":         "America/New_York",
		"residence":             "US",
		"app_language":          "en",
		"timezone_offset":       "-14400",
		"host_abi":              "armeabi-v7a",
		"locale":                "en",
		"ac2":                   "wifi5g",
		"uoo":                   "1",
		"carrier_region":        "US",
		"op_region":             "US",
		"build_number":          "35.1.3",
		"region":                "US",
		"ts":                    strconv.FormatInt(now.Unix(), 10),
		"device_id":             randomDigits(19),
		"openudid":              randomHex(16),
	}
}

func tikTokMobileUserAgent() string {
	return "com.zhiliaoapp.musically/2023501030 (Linux; U; Android 13; en_US; Pixel 7; Build/TD1A.220804.031; Cronet/58.0.2991.0)"
}

func randomDigits(length int) string {
	const digits = "0123456789"
	return randomString(length, digits)
}

func randomHex(length int) string {
	const hex = "0123456789abcdef"
	return randomString(length, hex)
}

func randomUUID() string {
	value := randomHex(32)
	return fmt.Sprintf("%s-%s-%s-%s-%s", value[:8], value[8:12], value[12:16], value[16:20], value[20:])
}

func randomString(length int, alphabet string) string {
	if length <= 0 {
		return ""
	}
	body := make([]byte, length)
	if _, err := cryptorand.Read(body); err != nil {
		seed := strconv.FormatInt(time.Now().UnixNano(), 16)
		for len(seed) < length {
			seed += seed
		}
		return seed[:length]
	}
	for i, value := range body {
		body[i] = alphabet[int(value)%len(alphabet)]
	}
	return string(body)
}

func tikTokPhotoAudioTimingFromMetadata(metadata tikTokPhotoAPIResponse) tikTokPhotoAudioTiming {
	timing := tikTokPhotoAudioTiming{}
	if metadata.Data.MusicBeginTimeMS != nil {
		timing.BeginMS = *metadata.Data.MusicBeginTimeMS
		timing.HasBegin = true
	}
	if metadata.Data.MusicEndTimeMS != nil {
		timing.EndMS = *metadata.Data.MusicEndTimeMS
		timing.HasEnd = true
	}
	return timing
}

func extractTikTokAudioTiming(metadata any) tikTokPhotoAudioTiming {
	timing := tikTokPhotoAudioTiming{}
	walkJSON(metadata, func(key string, value any) {
		normalized := strings.ToLower(key)
		switch normalized {
		case "music_begin_time_in_ms":
			if timing.HasBegin {
				return
			}
			ms, ok := jsonNumberOK(value)
			if !ok {
				return
			}
			timing.BeginMS = ms
			timing.HasBegin = true
		case "music_end_time_in_ms":
			if timing.HasEnd {
				return
			}
			ms, ok := jsonNumberOK(value)
			if !ok {
				return
			}
			timing.EndMS = ms
			timing.HasEnd = true
		}
	})
	return timing
}

func mergeTikTokPhotoAudioTiming(selection instagramAudioSelection, timing tikTokPhotoAudioTiming) instagramAudioSelection {
	if !timing.valid() {
		return selection
	}
	start := millisecondsDuration(timing.BeginMS)
	end := millisecondsDuration(timing.EndMS)
	selection.HasStartOffset = true
	selection.StartOffset = start
	selection.Duration = minDuration(end-start, photoAudioMaxDuration)
	selection.HasDuration = true
	return selection
}

func millisecondsDuration(ms float64) time.Duration {
	whole := int64(ms)
	fractional := ms - float64(whole)
	return time.Duration(whole)*time.Millisecond + time.Duration(fractional*float64(time.Millisecond))
}

func (t tikTokPhotoAudioTiming) valid() bool {
	return t.HasBegin && t.HasEnd && t.BeginMS >= 0 && t.EndMS > t.BeginMS
}

func tikTokPhotoAudioDuration(metadata tikTokPhotoAPIResponse) time.Duration {
	seconds := metadata.Data.MusicInfo.Duration
	if seconds <= 0 {
		seconds = metadata.Data.Duration
	}
	if seconds <= 0 {
		return photoAudioMaxDuration
	}
	return minDuration(time.Duration(seconds*float64(time.Second)), photoAudioMaxDuration)
}

func (d Downloader) fetchInstagramPage(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create instagram page request: %w", err)
	}
	req.Header.Set("User-Agent", instagramUserAgent)
	if err := d.addInstagramCookies(req); err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch instagram page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch instagram page: status %s", resp.Status)
	}

	body, err := readBounded(resp.Body, minPositive(d.MaxBytes, 5_000_000))
	if err != nil {
		return "", fmt.Errorf("read instagram page: %w", err)
	}
	return string(body), nil
}

func (d Downloader) fetchBoundedURL(ctx context.Context, rawURL string, referer string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create media request: %w", err)
	}
	req.Header.Set("User-Agent", instagramUserAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if err := d.addInstagramCookies(req); err != nil {
		return nil, "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("fetch media: status %s", resp.Status)
	}

	body, err := readBounded(resp.Body, d.MaxBytes)
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func (d Downloader) fetchInstagramMediaInfo(ctx context.Context, rawURL string) (any, error) {
	shortcode := instagramShortcode(rawURL)
	if shortcode == "" {
		return nil, fmt.Errorf("instagram media info: shortcode not found")
	}
	mediaID, ok := instagramMediaID(shortcode)
	if !ok {
		return nil, fmt.Errorf("instagram media info: parse shortcode")
	}

	baseURL := d.InstagramAPIBaseURL
	if baseURL == "" {
		baseURL = instagramAPIBaseURL
	}
	endpoint, err := url.JoinPath(baseURL, "api", "v1", "media", mediaID, "info")
	if err != nil {
		return nil, fmt.Errorf("instagram media info URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create instagram media info request: %w", err)
	}
	req.Header.Set("User-Agent", instagramUserAgent)
	req.Header.Set("X-IG-App-ID", "936619743392459")
	req.Header.Set("Accept", "application/json")
	if err := d.addInstagramCookies(req); err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch instagram media info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusFound {
		return nil, ErrInstagramCookiesRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch instagram media info: status %s", resp.Status)
	}

	body, err := readBounded(resp.Body, minPositive(d.MaxBytes, 10_000_000))
	if err != nil {
		return nil, fmt.Errorf("read instagram media info: %w", err)
	}

	var metadata any
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("parse instagram media info: %w", err)
	}
	return metadata, nil
}

func (d Downloader) addInstagramCookies(req *http.Request) error {
	if d.InstagramCookiesFile == "" {
		return nil
	}
	cookies, err := readNetscapeCookies(d.InstagramCookiesFile, req.URL.Hostname())
	if err != nil {
		return err
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	return nil
}

func (d Downloader) synthesizeVideo(ctx context.Context, image []byte, audio []byte, audioStart time.Duration, duration time.Duration) ([]byte, error) {
	return d.synthesizeSlideshowVideo(ctx, [][]byte{image}, audio, audioStart, duration, "unknown")
}

func (d Downloader) synthesizeSlideshowVideo(ctx context.Context, images [][]byte, audio []byte, audioStart time.Duration, audioDuration time.Duration, platform string) ([]byte, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("synthesize slideshow: no images")
	}

	dir, err := os.MkdirTemp("", "reelsovoz-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	imagePaths := make([]string, 0, len(images))
	for i, image := range images {
		imagePath := filepath.Join(dir, fmt.Sprintf("image-%d.jpg", i))
		if err := os.WriteFile(imagePath, image, 0o600); err != nil {
			return nil, fmt.Errorf("write temp image %d: %w", i+1, err)
		}
		imagePaths = append(imagePaths, imagePath)
	}
	audioPath := filepath.Join(dir, "audio")
	outputPath := filepath.Join(dir, "output.mp4")
	if err := os.WriteFile(audioPath, audio, 0o600); err != nil {
		return nil, fmt.Errorf("write temp audio: %w", err)
	}

	if audioStart < 0 {
		audioStart = 0
	}
	if audioDuration <= 0 {
		audioDuration = photoAudioMaxDuration
	}
	videoDuration := slideshowVideoDuration(audioDuration, len(images))

	args := []string{
		"-hide_banner", "-y",
	}
	frameRate := photoVideoFrameRateForImageCount(len(images))
	for _, imagePath := range imagePaths {
		if len(images) <= 1 {
			args = append(args,
				"-framerate", strconv.Itoa(frameRate),
				"-loop", "1",
				"-t", ffmpegSeconds(videoDuration),
				"-i", imagePath,
			)
			continue
		}
		args = append(args, "-i", imagePath)
	}
	args = append(args,
		"-i", audioPath,
		"-filter_complex", slideshowFilterComplex(len(images), audioStart, audioDuration, videoDuration, images),
		"-map", "[vout]",
		"-map", "[a]",
		"-t", ffmpegSeconds(videoDuration),
		"-r", strconv.Itoa(frameRate),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "stillimage",
		"-crf", "32",
		"-pix_fmt", "yuv420p",
		"-color_range", "tv",
		"-c:a", "aac",
		"-movflags", "+faststart",
		outputPath,
	)

	var stderr cappedBuffer
	cmd := exec.CommandContext(ctx, d.ffmpegExecutable(), args...)
	cmd.Stderr = &stderr
	operation := ffmpegPhotoOperation(len(images))
	started := time.Now()
	err = cmd.Run()
	d.recordFFmpeg(platform, operation, ffmpegStatus(ctx, err), time.Since(started))
	if err != nil {
		return nil, commandError("ffmpeg synthesize", err, stderr.String(), ctx.Err())
	}

	file, err := os.Open(outputPath)
	if err != nil {
		return nil, fmt.Errorf("open synthesized video: %w", err)
	}
	defer file.Close()
	body, err := readBounded(file, d.MaxBytes)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (d Downloader) recordFFmpeg(platform string, operation string, status string, duration time.Duration) {
	if d.Metrics != nil {
		d.Metrics.ObserveFFmpeg(platform, operation, status, duration)
	}
}

func ffmpegPhotoOperation(imageCount int) string {
	if imageCount <= 1 {
		return "single_photo_audio"
	}
	return "slideshow_photo_audio"
}

func ffmpegStatus(ctx context.Context, err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func photoVideoFrameRateForImageCount(imageCount int) int {
	if imageCount <= 1 {
		return singlePhotoFrameRate
	}
	return photoVideoFrameRate
}

func downloadedMediaBytes(media []DownloadedMedia) [][]byte {
	values := make([][]byte, 0, len(media))
	for _, item := range media {
		values = append(values, item.Bytes)
	}
	return values
}

func slideshowFilterComplex(imageCount int, audioStart time.Duration, audioDuration time.Duration, videoDuration time.Duration, images [][]byte) string {
	audioInput := imageCount
	audioFilter := slideshowAudioFilter(audioInput, audioStart, audioDuration, videoDuration)
	if imageCount <= 1 {
		videoFilter := fmt.Sprintf("[0:v]scale=w=min(%d\\,iw):h=min(%d\\,ih):force_original_aspect_ratio=decrease:force_divisible_by=2:out_range=tv,format=yuv420p,setsar=1[vout]", photoVideoMaxDimension, photoVideoMaxDimension)
		return videoFilter + ";" + audioFilter
	}

	width, height := slideshowCanvasSize(images)
	var filter strings.Builder
	for i := 0; i < imageCount; i++ {
		if i > 0 {
			filter.WriteString(";")
		}
		if i == 0 {
			filter.WriteString(fmt.Sprintf("[%d:v]scale=w=min(%d\\,iw):h=min(%d\\,ih):force_original_aspect_ratio=decrease:force_divisible_by=2:out_range=tv,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,format=yuv420p,split=2[v0][v%d]", i, width, height, width, height, imageCount))
			continue
		}
		filter.WriteString(fmt.Sprintf("[%d:v]scale=w=min(%d\\,iw):h=min(%d\\,ih):force_original_aspect_ratio=decrease:force_divisible_by=2:out_range=tv,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,format=yuv420p[v%d]", i, width, height, width, height, i))
	}

	filter.WriteString(";")
	for i := 0; i < imageCount; i++ {
		filter.WriteString(fmt.Sprintf("[v%d]", i))
	}
	filter.WriteString(fmt.Sprintf("[v%d]", imageCount))
	filter.WriteString(fmt.Sprintf("hstack=inputs=%d[strip]", imageCount+1))
	filter.WriteString(";")
	filter.WriteString(fmt.Sprintf("[strip]loop=loop=-1:size=1:start=0,trim=duration=%s,setpts=N/(%d*TB),crop=w=%d:h=%d:x='%s':y=0,scale=out_range=tv,format=yuv420p[vout]", ffmpegSeconds(videoDuration), photoVideoFrameRate, width, height, slideshowCropExpression(width, imageCount)))
	filter.WriteString(";")
	filter.WriteString(audioFilter)
	return filter.String()
}

func slideshowAudioFilter(audioInput int, audioStart time.Duration, audioDuration time.Duration, videoDuration time.Duration) string {
	base := fmt.Sprintf("[%d:a]atrim=start=%s:duration=%s,asetpts=PTS-STARTPTS", audioInput, ffmpegSeconds(audioStart), ffmpegSeconds(audioDuration))
	if audioDuration >= videoDuration {
		return base + "[a]"
	}
	loopSamples := audioLoopSamples(audioDuration)
	return fmt.Sprintf("%s,aresample=%d,aloop=loop=-1:size=%d,atrim=duration=%s,asetpts=PTS-STARTPTS[a]", base, audioLoopSampleRate, loopSamples, ffmpegSeconds(videoDuration))
}

func audioLoopSamples(duration time.Duration) int64 {
	samples := int64(duration) * int64(audioLoopSampleRate) / int64(time.Second)
	if samples < 1 {
		return 1
	}
	return samples
}

func slideshowCropExpression(width int, imageCount int) string {
	if imageCount <= 1 || width <= 0 {
		return "0"
	}
	transition := minDuration(photoSlideTransition, photoSlideDuration)
	hold := photoSlideDuration - transition
	cycle := time.Duration(imageCount) * photoSlideDuration
	return fmt.Sprintf(
		"%d*(floor(mod(t\\,%s)/%s)+clip((mod(mod(t\\,%s)\\,%s)-%s)/%s\\,0\\,1))",
		width,
		ffmpegSeconds(cycle),
		ffmpegSeconds(photoSlideDuration),
		ffmpegSeconds(cycle),
		ffmpegSeconds(photoSlideDuration),
		ffmpegSeconds(hold),
		ffmpegSeconds(transition),
	)
}

func slideshowVideoDuration(audioDuration time.Duration, imageCount int) time.Duration {
	if audioDuration <= 0 {
		audioDuration = photoAudioMaxDuration
	}
	if imageCount <= 1 {
		return audioDuration
	}
	minimum := time.Duration(imageCount) * photoSlideDuration
	if audioDuration < minimum {
		return minimum
	}
	return audioDuration
}

func extractInstagramImageURLs(page string) []string {
	values := extractMetaContent(page, `og:image`)
	values = append(values, extractJSONishURLs(page, "display_url")...)
	values = append(values, extractJSONishURLs(page, "thumbnail_src")...)
	return values
}

func extractInstagramAudioURLs(page string) []string {
	values := extractJSONishURLs(page, "audio_url")
	values = append(values, extractJSONishURLs(page, "progressive_download_url")...)
	values = append(values, extractMediaURLsFromDashManifests(extractJSONishURLs(page, "dash_manifest"))...)
	return values
}

func hasInstagramMusicMarker(page string) bool {
	lower := strings.ToLower(page)
	for key := range instagramMusicMarkerKeys {
		if strings.Contains(lower, strings.ToLower(key)) {
			return true
		}
	}
	return false
}

func extractInstagramMetadataImageURLs(metadata any) []string {
	var values []string
	for _, item := range metadataItems(metadata) {
		if imageURL := bestImageCandidateURL(item); imageURL != "" {
			values = append(values, imageURL)
		}
		for _, carouselItem := range metadataArray(item, "carousel_media") {
			if imageURL := bestImageCandidateURL(carouselItem); imageURL != "" {
				values = append(values, imageURL)
			}
		}
	}
	return uniqueStrings(values)
}

func extractInstagramMetadataAudioURLs(metadata any) []string {
	return extractInstagramMetadataAudioSelection(metadata).URLs()
}

func extractInstagramMetadataAudioSelection(metadata any) instagramAudioSelection {
	values := extractInstagramMetadataURLs(metadata, instagramDirectAudioFieldKeys, false)
	values = append(values, extractMediaURLsFromDashManifests(values)...)
	values = uniqueStrings(values)

	selection := instagramAudioSelection{URL: firstString(values)}
	walkJSON(metadata, func(key string, value any) {
		normalized := strings.ToLower(key)
		switch normalized {
		case "audio_asset_start_time_in_ms":
			if selection.HasStartOffset {
				return
			}
			ms, ok := jsonNumberOK(value)
			if !ok || ms < 0 {
				return
			}
			selection.StartOffset = time.Duration(ms * float64(time.Millisecond))
			selection.HasStartOffset = true
		case "overlap_duration_in_ms":
			if selection.HasDuration {
				return
			}
			ms, ok := jsonNumberOK(value)
			if !ok || ms <= 0 {
				return
			}
			selection.Duration = time.Duration(ms * float64(time.Millisecond))
			selection.HasDuration = true
		}
	})
	return selection
}

func (s instagramAudioSelection) URLs() []string {
	if s.URL == "" {
		return nil
	}
	return []string{s.URL}
}

func mergeInstagramAudioSelection(base instagramAudioSelection, metadata instagramAudioSelection) instagramAudioSelection {
	if metadata.URL != "" {
		base.URL = metadata.URL
	}
	if metadata.HasStartOffset {
		base.StartOffset = metadata.StartOffset
		base.HasStartOffset = true
	}
	if metadata.HasDuration {
		base.Duration = metadata.Duration
		base.HasDuration = true
	}
	return base
}

func hasInstagramMetadataMusicMarker(metadata any) bool {
	found := false
	walkJSON(metadata, func(key string, value any) {
		if found {
			return
		}
		normalized := strings.ToLower(key)
		if _, ok := instagramMusicMarkerKeys[normalized]; ok {
			found = true
			return
		}
		if normalized == "product_type" {
			if text, ok := value.(string); ok && strings.Contains(strings.ToLower(text), "clips") {
				found = true
			}
		}
	})
	return found
}

func extractInstagramMetadataURLs(metadata any, allowedKeys map[string]struct{}, imagesOnly bool) []string {
	var values []string
	walkJSON(metadata, func(key string, value any) {
		if _, ok := allowedKeys[strings.ToLower(key)]; !ok {
			return
		}
		text, ok := value.(string)
		if !ok {
			return
		}
		text = normalizeHTMLValue(unescapeJSONish(text))
		if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
			return
		}
		if imagesOnly && !looksLikeImageURL(text) {
			return
		}
		values = append(values, text)
	})
	return uniqueStrings(values)
}

func walkJSON(value any, visit func(key string, value any)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			visit(key, child)
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkJSON(child, visit)
		}
	}
}

func extractMediaURLsFromDashManifests(values []string) []string {
	var urls []string
	pattern := regexp.MustCompile(`https?://[^<>"'\s]+`)
	for _, value := range values {
		for _, match := range pattern.FindAllString(value, -1) {
			match = strings.TrimRight(match, `,;`)
			urls = append(urls, normalizeHTMLValue(match))
		}
	}
	return urls
}

func looksLikeImageURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, ".jpg") ||
		strings.Contains(lower, ".jpeg") ||
		strings.Contains(lower, ".webp") ||
		strings.Contains(lower, ".png") ||
		strings.Contains(lower, "scontent")
}

func metadataItems(metadata any) []map[string]any {
	root, ok := metadata.(map[string]any)
	if !ok {
		return nil
	}
	items := metadataArray(root, "items")
	if len(items) > 0 {
		return items
	}
	return []map[string]any{root}
}

func metadataArray(parent map[string]any, key string) []map[string]any {
	raw, ok := parent[key].([]any)
	if !ok {
		return nil
	}
	values := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if typed, ok := item.(map[string]any); ok {
			values = append(values, typed)
		}
	}
	return values
}

func bestImageCandidateURL(item map[string]any) string {
	imageVersions, ok := item["image_versions2"].(map[string]any)
	if !ok {
		return ""
	}
	candidates, ok := imageVersions["candidates"].([]any)
	if !ok {
		return ""
	}
	var bestURL string
	var bestArea float64
	for _, candidate := range candidates {
		typed, ok := candidate.(map[string]any)
		if !ok {
			continue
		}
		rawURL, ok := typed["url"].(string)
		if !ok {
			continue
		}
		rawURL = normalizeHTMLValue(unescapeJSONish(rawURL))
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			continue
		}
		area := jsonNumber(typed["width"]) * jsonNumber(typed["height"])
		if bestURL == "" || area > bestArea {
			bestURL = rawURL
			bestArea = area
		}
	}
	return bestURL
}

func jsonNumber(value any) float64 {
	number, _ := jsonNumberOK(value)
	return number
}

func jsonNumberOK(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func extractMetaContent(page string, property string) []string {
	pattern := regexp.MustCompile(`(?is)<meta\s+[^>]*(?:property|name)=["']` + regexp.QuoteMeta(property) + `["'][^>]*content=["']([^"']+)["'][^>]*>`)
	matches := pattern.FindAllStringSubmatch(page, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, normalizeHTMLValue(match[1]))
	}
	return values
}

func extractJSONishURLs(page string, key string) []string {
	pattern := regexp.MustCompile(`(?s)(?:\\"|")` + regexp.QuoteMeta(key) + `(?:\\"|")\s*:\s*(?:\\"|")([^"\\]*(?:\\.[^"\\]*)*)(?:\\"|")`)
	matches := pattern.FindAllStringSubmatch(page, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, normalizeHTMLValue(unescapeJSONish(match[1])))
	}
	return values
}

func normalizeHTMLValue(value string) string {
	return strings.TrimSpace(html.UnescapeString(value))
}

func unescapeJSONish(value string) string {
	value = strings.ReplaceAll(value, `\/`, `/`)
	value = strings.ReplaceAll(value, `\u0026`, `&`)
	value = strings.ReplaceAll(value, `\&`, `&`)
	value = strings.ReplaceAll(value, `\"`, `"`)
	return value
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func firstString(values []string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func imageSize(body []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func scaledImageSize(body []byte) (int, int) {
	width, height := imageSize(body)
	return scaledDimensions(width, height, photoVideoMaxDimension)
}

func slideshowCanvasSize(images [][]byte) (int, int) {
	if len(images) == 0 {
		return 0, 0
	}
	if len(images) == 1 {
		return scaledImageSize(images[0])
	}

	var canvasWidth int
	var canvasHeight int
	for _, image := range images {
		width, height := scaledImageSize(image)
		if width > canvasWidth {
			canvasWidth = width
		}
		if height > canvasHeight {
			canvasHeight = height
		}
	}
	if canvasWidth == 0 || canvasHeight == 0 {
		return photoVideoMaxDimension, photoVideoMaxDimension
	}
	return evenPositive(canvasWidth), evenPositive(canvasHeight)
}

func scaledDimensions(width int, height int, maxDimension int) (int, int) {
	if width <= 0 || height <= 0 || maxDimension <= 0 {
		return 0, 0
	}

	scale := 1.0
	scaled := false
	if width > maxDimension || height > maxDimension {
		scaled = true
		if width >= height {
			scale = float64(maxDimension) / float64(width)
		} else {
			scale = float64(maxDimension) / float64(height)
		}
	}

	scaledWidth := int(float64(width) * scale)
	scaledHeight := int(float64(height) * scale)
	if scaled {
		return evenScaled(scaledWidth, maxDimension), evenScaled(scaledHeight, maxDimension)
	}
	return evenPositive(scaledWidth), evenPositive(scaledHeight)
}

func evenPositive(value int) int {
	if value < 2 {
		return 2
	}
	if value%2 != 0 {
		value--
	}
	if value < 2 {
		return 2
	}
	return value
}

func evenScaled(value int, maxDimension int) int {
	if value < 2 {
		return 2
	}
	if value%2 == 0 {
		return value
	}
	if value < maxDimension {
		return value + 1
	}
	return value - 1
}

func ffmpegSeconds(duration time.Duration) string {
	if duration%time.Millisecond == 0 {
		ms := duration / time.Millisecond
		sign := ""
		if ms < 0 {
			sign = "-"
			ms = -ms
		}
		seconds := ms / 1000
		milliseconds := ms % 1000
		if milliseconds == 0 {
			return sign + strconv.FormatInt(int64(seconds), 10)
		}
		return fmt.Sprintf("%s%d.%03d", sign, seconds, milliseconds)
	}
	return strconv.FormatFloat(duration.Seconds(), 'f', -1, 64)
}

func minDuration(a time.Duration, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func minPositive(a int64, b int64) int64 {
	if a <= 0 {
		return b
	}
	if a < b {
		return a
	}
	return b
}

func instagramShortcode(rawURL string) string {
	value := rawURL
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) < 2 || parts[0] != "p" {
		return ""
	}
	return parts[1]
}

func instagramMediaID(shortcode string) (string, bool) {
	var id uint64
	for _, ch := range shortcode {
		index := strings.IndexRune(instagramShortcodeAlphabet, ch)
		if index < 0 {
			return "", false
		}
		id = id*64 + uint64(index)
	}
	return strconv.FormatUint(id, 10), true
}

func readNetscapeCookies(path string, host string) ([]*http.Cookie, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read instagram cookies: %w", err)
	}

	lines := strings.Split(string(body), "\n")
	cookies := make([]*http.Cookie, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		domain := strings.TrimPrefix(fields[0], ".")
		if host != domain && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:  fields[5],
			Value: sanitizeNetscapeCookieValue(fields[6]),
		})
	}
	return cookies, nil
}

func sanitizeNetscapeCookieValue(value string) string {
	value = strings.Trim(value, `"`)
	return strings.Map(func(r rune) rune {
		switch r {
		case '"', ';', '\\':
			return -1
		default:
			return r
		}
	}, value)
}
