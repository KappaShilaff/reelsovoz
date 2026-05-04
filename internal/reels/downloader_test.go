package reels

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloaderDownloadsMP4WithMetadata(t *testing.T) {
	fake := newFakeYTDLP(t)

	got, err := Downloader{
		YTDLPPath: fake.path,
		Timeout:   time.Second,
		MaxBytes:  64,
	}.Download(context.Background(), "https://www.tiktok.com/@user/video/123")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Kind != MediaKindVideo {
		t.Fatalf("Kind = %q, want video", got[0].Kind)
	}
	if string(got[0].Bytes) != "mp4-bytes" {
		t.Fatalf("Bytes = %q", got[0].Bytes)
	}
	if got[0].Filename != "reel.mp4" {
		t.Fatalf("Filename = %q", got[0].Filename)
	}
	if got[0].MIME != "video/mp4" {
		t.Fatalf("MIME = %q", got[0].MIME)
	}
	if got[0].Title != "Test Reel" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Duration != 12.5 {
		t.Fatalf("Duration = %f", got[0].Duration)
	}
	if got[0].Width != 720 || got[0].Height != 1280 {
		t.Fatalf("size = %dx%d", got[0].Width, got[0].Height)
	}

	calls := fake.calls(t)
	if len(calls) != 2 {
		t.Fatalf("calls = %v, want metadata and download", calls)
	}
	if !strings.Contains(calls[0], "-j") {
		t.Fatalf("metadata args = %q, want -j", calls[0])
	}
	if !strings.Contains(calls[1], "--output -") {
		t.Fatalf("download args = %q, want stdout output", calls[1])
	}
	if strings.Contains(calls[1], "--cookies") {
		t.Fatalf("download args = %q, did not expect cookies", calls[1])
	}
}

func TestDownloaderUsesInstagramCookiesOnlyForInstagram(t *testing.T) {
	fake := newFakeYTDLP(t)

	_, err := Downloader{
		YTDLPPath:            fake.path,
		InstagramCookiesFile: "/tmp/ig-cookies.txt",
		Timeout:              time.Second,
		MaxBytes:             64,
	}.Download(context.Background(), "https://instagram.com/reel/ABC/")
	if err != nil {
		t.Fatalf("Download(instagram) error = %v", err)
	}

	calls := fake.calls(t)
	for _, call := range calls {
		if !strings.Contains(call, "--cookies /tmp/ig-cookies.txt") {
			t.Fatalf("instagram call = %q, want cookies", call)
		}
	}

	fake.truncateCalls(t)
	_, err = Downloader{
		YTDLPPath:            fake.path,
		InstagramCookiesFile: "/tmp/ig-cookies.txt",
		Timeout:              time.Second,
		MaxBytes:             64,
	}.Download(context.Background(), "https://vm.tiktok.com/ZMabc/")
	if err != nil {
		t.Fatalf("Download(tiktok) error = %v", err)
	}

	for _, call := range fake.calls(t) {
		if strings.Contains(call, "--cookies") {
			t.Fatalf("tiktok call = %q, did not expect cookies", call)
		}
	}
}

func TestDownloaderCancelsOnOverflow(t *testing.T) {
	fake := newFakeYTDLP(t)
	t.Setenv("FAKE_YTDLP_DOWNLOAD", "0123456789")

	_, err := Downloader{
		YTDLPPath: fake.path,
		Timeout:   time.Second,
		MaxBytes:  5,
	}.Download(context.Background(), "https://www.tiktok.com/@user/video/123")
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("Download() error = %v, want %v", err, ErrDownloadTooLarge)
	}
}

func TestDownloaderCapsStderrDiagnostics(t *testing.T) {
	fake := newFakeYTDLP(t)
	t.Setenv("FAKE_YTDLP_METADATA_FAIL", "1")

	_, err := Downloader{
		YTDLPPath: fake.path,
		Timeout:   time.Second,
		MaxBytes:  64,
	}.Download(context.Background(), "https://www.tiktok.com/@user/video/123")
	if err == nil {
		t.Fatal("Download() error = nil, want metadata failure")
	}

	message := err.Error()
	if len(message) > maxStderrBytes+256 {
		t.Fatalf("error length = %d, want capped stderr", len(message))
	}
	if !strings.Contains(message, "metadata-failed") {
		t.Fatalf("error = %q, want stderr detail", message)
	}
}

func TestDownloaderSynthesizesTikTokPhotoPostWithAudio(t *testing.T) {
	fake := newFakeYTDLP(t)
	ffmpeg := newFakeFFmpeg(t, "tiktok-photo-mp4")
	t.Setenv("FAKE_YTDLP_METADATA_UNSUPPORTED_TIKTOK_PHOTO", "1")

	var sawAPIURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			sawAPIURL = r.URL.Query().Get("url")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"code": 0,
				"msg": "success",
				"data": {
					"title": "TikTok Photo",
					"images": ["` + serverURL(r, "/image.jpg") + `"],
					"music_info": {
						"play": "` + serverURL(r, "/audio.mp3") + `",
						"duration": 53
					}
				}
			}`))
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("audio-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rawURL := "https://vt.tiktok.com/ZS9Sbqjw9/"
	got, err := Downloader{
		YTDLPPath:        fake.path,
		FFmpegPath:       ffmpeg.path,
		TikTokAPIBaseURL: server.URL,
		MaxBytes:         200_000,
	}.Download(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if sawAPIURL != rawURL {
		t.Fatalf("api url = %q, want %q", sawAPIURL, rawURL)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Kind != MediaKindVideo {
		t.Fatalf("Kind = %q, want video", got[0].Kind)
	}
	if string(got[0].Bytes) != "tiktok-photo-mp4" {
		t.Fatalf("Bytes = %q", got[0].Bytes)
	}
	if got[0].Title != "TikTok Photo" {
		t.Fatalf("Title = %q, want TikTok Photo", got[0].Title)
	}
	if got[0].Duration != 53 {
		t.Fatalf("Duration = %f, want 53", got[0].Duration)
	}
	if got[0].Width != 480 || got[0].Height != 270 {
		t.Fatalf("size = %dx%d, want 480x270", got[0].Width, got[0].Height)
	}

	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0], "atrim=start=0:duration=53") {
		t.Fatalf("ffmpeg args = %q, want default tiktok photo audio trim", calls[0])
	}
	if !strings.Contains(calls[0], "scale=w=min(480\\,iw):h=min(480\\,ih):force_original_aspect_ratio=decrease:force_divisible_by=2") {
		t.Fatalf("ffmpeg args = %q, want 480p no-crop scale filter", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 1, 1)
}

func TestDownloaderSynthesizesTikTokPhotoCarouselWithAudio(t *testing.T) {
	fake := newFakeYTDLP(t)
	ffmpeg := newFakeFFmpeg(t, "tiktok-carousel-mp4")
	t.Setenv("FAKE_YTDLP_METADATA_UNSUPPORTED_TIKTOK_PHOTO", "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"code": 0,
				"msg": "success",
				"data": {
					"title": "TikTok Carousel",
					"images": [
						"` + serverURL(r, "/first.jpg") + `",
						"` + serverURL(r, "/second.jpg") + `",
						"` + serverURL(r, "/third.jpg") + `",
						"` + serverURL(r, "/fourth.jpg") + `",
						"` + serverURL(r, "/fifth.jpg") + `",
						"` + serverURL(r, "/sixth.jpg") + `",
						"` + serverURL(r, "/seventh.jpg") + `"
					],
					"music_info": {
						"play": "` + serverURL(r, "/audio.mp3") + `",
						"duration": 45
					}
				}
			}`))
		case "/first.jpg", "/second.jpg", "/third.jpg", "/fourth.jpg", "/fifth.jpg", "/sixth.jpg", "/seventh.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("audio-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{
		YTDLPPath:        fake.path,
		FFmpegPath:       ffmpeg.path,
		TikTokAPIBaseURL: server.URL,
		MaxBytes:         200_000,
	}.Download(context.Background(), "https://vt.tiktok.com/ZS9Sbqjw9/")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Kind != MediaKindVideo {
		t.Fatalf("Kind = %q, want video", got[0].Kind)
	}
	if got[0].Duration != 45 {
		t.Fatalf("Duration = %f, want 45", got[0].Duration)
	}
	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if strings.Count(calls[0], "-loop 1") != 7 {
		t.Fatalf("ffmpeg args = %q, want seven image inputs", calls[0])
	}
	if strings.Contains(calls[0], "xfade=transition=slideleft") {
		t.Fatalf("ffmpeg args = %q, did not expect xfade", calls[0])
	}
	if !strings.Contains(calls[0], "hstack=inputs=7") {
		t.Fatalf("ffmpeg args = %q, want seven-image strip", calls[0])
	}
	if !strings.Contains(calls[0], "crop=w=") {
		t.Fatalf("ffmpeg args = %q, want moving crop viewport", calls[0])
	}
	if !strings.Contains(calls[0], "clip((t-23.500)/0.500\\,0\\,1)") {
		t.Fatalf("ffmpeg args = %q, want final slide motion before remaining time", calls[0])
	}
	if !strings.Contains(calls[0], "atrim=start=0:duration=45") {
		t.Fatalf("ffmpeg args = %q, want 45s audio trim", calls[0])
	}
	if !strings.Contains(calls[0], "-pix_fmt yuv420p") {
		t.Fatalf("ffmpeg args = %q, want yuv420p output", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 7, 6)
}

func TestDownloaderSynthesizesTikTokPhotoPostWithAudioOffsetFromMetadata(t *testing.T) {
	fake := newFakeYTDLP(t)
	ffmpeg := newFakeFFmpeg(t, "tiktok-photo-offset-mp4")
	t.Setenv("FAKE_YTDLP_METADATA_UNSUPPORTED_TIKTOK_PHOTO", "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"code": 0,
				"msg": "success",
				"data": {
					"title": "TikTok Photo Offset",
					"images": ["` + serverURL(r, "/image.jpg") + `"],
					"music_begin_time_in_ms": 17548,
					"music_end_time_in_ms": 27215,
					"music_info": {
						"play": "` + serverURL(r, "/audio.mp3") + `",
						"duration": 53
					}
				}
			}`))
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("audio-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{
		YTDLPPath:        fake.path,
		FFmpegPath:       ffmpeg.path,
		TikTokAPIBaseURL: server.URL,
		MaxBytes:         200_000,
	}.Download(context.Background(), "https://vt.tiktok.com/ZS9Sbqjw9/")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Duration < 9.666 || got[0].Duration > 9.668 {
		t.Fatalf("Duration = %f, want 9.667", got[0].Duration)
	}
	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0], "atrim=start=17.548:duration=9.667") {
		t.Fatalf("ffmpeg args = %q, want tiktok metadata audio trim", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 1, 1)
}

func TestDownloaderSynthesizesTikTokPhotoPostWithAudioOffsetFromAwemeFallback(t *testing.T) {
	fake := newFakeYTDLP(t)
	ffmpeg := newFakeFFmpeg(t, "tiktok-photo-aweme-offset-mp4")
	t.Setenv("FAKE_YTDLP_METADATA_UNSUPPORTED_TIKTOK_PHOTO", "1")

	var sawAwemeID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"code": 0,
				"msg": "success",
				"data": {
					"id": "7633393932105288967",
					"title": "TikTok Aweme Offset",
					"images": ["` + serverURL(r, "/image.jpg") + `"],
					"music_info": {
						"play": "` + serverURL(r, "/audio.mp3") + `",
						"duration": 53
					}
				}
			}`))
		case "/aweme/v1/feed":
			sawAwemeID = r.URL.Query().Get("aweme_id")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status_code": 0,
				"aweme_list": [{
					"aweme_id": "7633393932105288967",
					"music_begin_time_in_ms": 17548,
					"music_end_time_in_ms": 27215
				}]
			}`))
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("audio-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{
		YTDLPPath:              fake.path,
		FFmpegPath:             ffmpeg.path,
		TikTokAPIBaseURL:       server.URL,
		TikTokMobileAPIBaseURL: server.URL,
		MaxBytes:               200_000,
	}.Download(context.Background(), "https://vt.tiktok.com/ZS9Sbqjw9/")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if sawAwemeID != "7633393932105288967" {
		t.Fatalf("aweme_id = %q, want 7633393932105288967", sawAwemeID)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Duration < 9.666 || got[0].Duration > 9.668 {
		t.Fatalf("Duration = %f, want 9.667", got[0].Duration)
	}
	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0], "atrim=start=17.548:duration=9.667") {
		t.Fatalf("ffmpeg args = %q, want aweme fallback audio trim", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 1, 1)
}

func TestDownloaderSynthesizesTikTokPhotoPostWithAudioOffsetFromPageFallback(t *testing.T) {
	ffmpeg := newFakeFFmpeg(t, "tiktok-photo-page-offset-mp4")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"code": 0,
				"msg": "success",
				"data": {
					"title": "TikTok Page Offset",
					"images": ["` + serverURL(r, "/image.jpg") + `"],
					"music_info": {
						"play": "` + serverURL(r, "/audio.mp3") + `",
						"duration": 53
					}
				}
			}`))
		case "/post":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">{
				"item": {
					"music_begin_time_in_ms": 17548,
					"music_end_time_in_ms": 27215
				}
			}</script></body></html>`))
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("audio-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{
		FFmpegPath:       ffmpeg.path,
		TikTokAPIBaseURL: server.URL,
		MaxBytes:         200_000,
	}.downloadTikTokPhotoPost(context.Background(), server.URL+"/post")
	if err != nil {
		t.Fatalf("downloadTikTokPhotoPost() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Duration < 9.666 || got[0].Duration > 9.668 {
		t.Fatalf("Duration = %f, want 9.667", got[0].Duration)
	}
	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0], "atrim=start=17.548:duration=9.667") {
		t.Fatalf("ffmpeg args = %q, want page fallback audio trim", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 1, 1)
}

func TestTikTokPhotoAudioSelectionClampsAndFallsBackTiming(t *testing.T) {
	begin := float64(1000)
	longEnd := float64(120000)
	selection := tikTokPhotoAudioSelection(tikTokPhotoAPIResponse{
		Data: tikTokPhotoData{
			Play:             "https://cdn.example/audio.mp3",
			MusicBeginTimeMS: &begin,
			MusicEndTimeMS:   &longEnd,
			MusicInfo:        tikTokMusicInfo{Duration: 53},
		},
	})
	if !selection.HasStartOffset || selection.StartOffset != time.Second {
		t.Fatalf("StartOffset = %v has=%v, want 1s", selection.StartOffset, selection.HasStartOffset)
	}
	if got := selection.effectiveDuration(); got != 90*time.Second {
		t.Fatalf("duration = %v, want 90s clamp", got)
	}

	invalidEnd := float64(500)
	fallback := tikTokPhotoAudioSelection(tikTokPhotoAPIResponse{
		Data: tikTokPhotoData{
			Play:             "https://cdn.example/audio.mp3",
			MusicBeginTimeMS: &begin,
			MusicEndTimeMS:   &invalidEnd,
			MusicInfo:        tikTokMusicInfo{Duration: 12},
		},
	})
	if fallback.HasStartOffset {
		t.Fatalf("StartOffset has=%v, want invalid timing ignored", fallback.HasStartOffset)
	}
	if got := fallback.effectiveDuration(); got != 12*time.Second {
		t.Fatalf("duration = %v, want metadata duration fallback", got)
	}
}

func TestDownloaderReturnsPhotosForTikTokPhotoPostWithoutAudio(t *testing.T) {
	fake := newFakeYTDLP(t)
	t.Setenv("FAKE_YTDLP_METADATA_UNSUPPORTED_TIKTOK_PHOTO", "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"code": 0,
				"msg": "success",
				"data": {
					"title": "TikTok Photos",
					"images": ["` + serverURL(r, "/first.jpg") + `", "` + serverURL(r, "/second.jpg") + `"]
				}
			}`))
		case "/first.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("first-jpg"))
		case "/second.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("second-jpg"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{
		YTDLPPath:        fake.path,
		TikTokAPIBaseURL: server.URL,
		MaxBytes:         1024,
	}.Download(context.Background(), "https://vt.tiktok.com/ZS9Sbqjw9/")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("items = %d, want 2", len(got))
	}
	if got[0].Kind != MediaKindPhoto || got[1].Kind != MediaKindPhoto {
		t.Fatalf("items = %#v, want photos", got)
	}
	if got[0].Filename != "photo-1.jpg" || got[1].Filename != "photo-2.jpg" {
		t.Fatalf("filenames = %q, %q", got[0].Filename, got[1].Filename)
	}
	if string(got[0].Bytes) != "first-jpg" || string(got[1].Bytes) != "second-jpg" {
		t.Fatalf("bytes = %q, %q", got[0].Bytes, got[1].Bytes)
	}
}

func TestDownloaderFallsBackForInstagramPhotoPostMetadataNoVideoError(t *testing.T) {
	fake := newFakeYTDLP(t)
	t.Setenv("FAKE_YTDLP_METADATA_NO_VIDEO", "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/DXfJlTsDNFr/":
			_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="` + serverURL(r, "/image.jpg") + `"></head></html>`))
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpg-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{
		YTDLPPath:           fake.path,
		InstagramAPIBaseURL: server.URL,
		MaxBytes:            1024,
	}.Download(context.Background(), server.URL+"/p/DXfJlTsDNFr/")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if len(got) != 1 || got[0].Kind != MediaKindPhoto {
		t.Fatalf("items = %#v, want single photo", got)
	}
}

func TestDownloaderDoesNotFallbackForReelNoVideoError(t *testing.T) {
	fake := newFakeYTDLP(t)
	t.Setenv("FAKE_YTDLP_METADATA_NO_VIDEO", "1")

	_, err := Downloader{
		YTDLPPath: fake.path,
		MaxBytes:  1024,
	}.Download(context.Background(), "https://instagram.com/reel/DXfJlTsDNFr/")
	if err == nil {
		t.Fatal("Download() error = nil, want metadata error")
	}
	if strings.Contains(err.Error(), "instagram photo post fallback") {
		t.Fatalf("Download() error = %v, did not expect photo fallback", err)
	}
}

func TestDownloaderFallsBackForInstagramPhotoPostNoVideoFormatsError(t *testing.T) {
	if !isInstagramPhotoPostFallback("https://instagram.com/p/DXfJlTsDNFr/", errors.New("yt-dlp metadata: No video formats found!")) {
		t.Fatal("expected photo post fallback for no video formats error")
	}
}

func TestDownloaderReturnsPhotoForInstagramPhotoPostWithoutAudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/post/":
			_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="Photo Post"><meta property="og:image" content="` + serverURL(r, "/image.jpg") + `"></head></html>`))
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpg-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{MaxBytes: 1024}.downloadInstagramPhotoPost(context.Background(), server.URL+"/p/post/")
	if err != nil {
		t.Fatalf("downloadInstagramPhotoPost() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Kind != MediaKindPhoto {
		t.Fatalf("Kind = %q, want photo", got[0].Kind)
	}
	if got[0].Filename != "photo.jpg" {
		t.Fatalf("Filename = %q", got[0].Filename)
	}
	if got[0].MIME != "image/jpeg" {
		t.Fatalf("MIME = %q", got[0].MIME)
	}
	if string(got[0].Bytes) != "jpg-bytes" {
		t.Fatalf("Bytes = %q", got[0].Bytes)
	}
}

func TestDownloaderReturnsAllPhotosForCarouselWithoutAudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/DXfJlTsDNFr/":
			_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="` + serverURL(r, "/first.jpg") + `"><script>{"display_url":"` + serverURL(r, "/second.jpg") + `"}</script></head></html>`))
		case "/first.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("first-jpg"))
		case "/second.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("second-jpg"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{InstagramAPIBaseURL: server.URL, MaxBytes: 1024}.downloadInstagramPhotoPost(context.Background(), server.URL+"/p/DXfJlTsDNFr/")
	if err != nil {
		t.Fatalf("downloadInstagramPhotoPost() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("items = %d, want 2", len(got))
	}
	if got[0].Filename != "photo-1.jpg" || got[1].Filename != "photo-2.jpg" {
		t.Fatalf("filenames = %q, %q", got[0].Filename, got[1].Filename)
	}
	if string(got[0].Bytes) != "first-jpg" || string(got[1].Bytes) != "second-jpg" {
		t.Fatalf("bytes = %q, %q", got[0].Bytes, got[1].Bytes)
	}
}

func TestDownloaderSynthesizesInstagramPhotoPostWithAudio(t *testing.T) {
	ffmpeg := newFakeFFmpeg(t, "synth-mp4")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/post/":
			_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="` + serverURL(r, "/image.jpg") + `"></head><script>{"audio_url":"` + serverURL(r, `/audio.m4a`) + `"}</script></html>`))
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.m4a":
			w.Header().Set("Content-Type", "audio/mp4")
			_, _ = w.Write([]byte("audio-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{FFmpegPath: ffmpeg.path, MaxBytes: 200_000}.downloadInstagramPhotoPost(context.Background(), server.URL+"/p/post/")
	if err != nil {
		t.Fatalf("downloadInstagramPhotoPost() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Kind != MediaKindVideo {
		t.Fatalf("Kind = %q, want video", got[0].Kind)
	}
	if string(got[0].Bytes) != "synth-mp4" {
		t.Fatalf("Bytes = %q", got[0].Bytes)
	}
	if got[0].Duration != 90 {
		t.Fatalf("Duration = %f, want 90", got[0].Duration)
	}
	if got[0].Width != 480 || got[0].Height != 270 {
		t.Fatalf("size = %dx%d, want 480x270", got[0].Width, got[0].Height)
	}
	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0], "atrim=start=0:duration=90") {
		t.Fatalf("ffmpeg args = %q, want default audio trim", calls[0])
	}
	if !strings.Contains(calls[0], "scale=w=min(480\\,iw):h=min(480\\,ih):force_original_aspect_ratio=decrease:force_divisible_by=2") {
		t.Fatalf("ffmpeg args = %q, want 480p no-crop scale filter", calls[0])
	}
	if strings.Contains(calls[0], "crop") {
		t.Fatalf("ffmpeg args = %q, did not expect crop", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 1, 1)
}

func TestDownloaderSynthesizesInstagramPhotoPostWithAuthenticatedMetadataAudio(t *testing.T) {
	ffmpeg := newFakeFFmpeg(t, "api-synth-mp4")
	var sawAPICookie bool
	var sawMediaCookie bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/DXfJlTsDNFr/":
			_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="` + serverURL(r, "/fallback.jpg") + `"></head></html>`))
		case "/api/v1/media/3881863549996028267/info":
			if cookie, err := r.Cookie("sessionid"); err == nil && cookie.Value == "cookie-value" {
				sawAPICookie = true
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [{
					"image_versions2": {"candidates": [{"url": "` + serverURL(r, "/api-image.jpg") + `"}]},
					"clips_metadata": {
						"music_info": {
							"music_asset_info": {
								"audio_asset_start_time_in_ms": 42000,
								"overlap_duration_in_ms": 17000,
								"progressive_download_url": "` + serverURL(r, "/audio.m4a") + `"
							}
						}
					}
				}]
			}`))
		case "/fallback.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("fallback-jpg"))
		case "/api-image.jpg":
			if cookie, err := r.Cookie("sessionid"); err == nil && cookie.Value == "cookie-value" {
				sawMediaCookie = true
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.m4a":
			if cookie, err := r.Cookie("sessionid"); err == nil && cookie.Value == "cookie-value" {
				sawMediaCookie = true
			}
			w.Header().Set("Content-Type", "audio/mp4")
			_, _ = w.Write([]byte("api-audio"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cookiesPath := writeCookiesFile(t, "127.0.0.1", "sessionid", "cookie-value")
	got, err := Downloader{
		FFmpegPath:           ffmpeg.path,
		InstagramCookiesFile: cookiesPath,
		InstagramAPIBaseURL:  server.URL,
		MaxBytes:             200_000,
	}.downloadInstagramPhotoPost(context.Background(), server.URL+"/p/DXfJlTsDNFr/")
	if err != nil {
		t.Fatalf("downloadInstagramPhotoPost() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Kind != MediaKindVideo {
		t.Fatalf("Kind = %q, want video", got[0].Kind)
	}
	if string(got[0].Bytes) != "api-synth-mp4" {
		t.Fatalf("Bytes = %q", got[0].Bytes)
	}
	if got[0].Duration != 17 {
		t.Fatalf("Duration = %f, want 17", got[0].Duration)
	}
	if got[0].Width != 480 || got[0].Height != 270 {
		t.Fatalf("size = %dx%d, want 480x270", got[0].Width, got[0].Height)
	}
	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0], "atrim=start=42:duration=17") {
		t.Fatalf("ffmpeg args = %q, want metadata audio trim", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 1, 1)
	if !sawAPICookie {
		t.Fatal("Instagram API request did not include cookies")
	}
	if !sawMediaCookie {
		t.Fatal("Instagram media request did not include cookies")
	}
}

func TestDownloaderExtractsInstagramAudioTimingMetadata(t *testing.T) {
	metadata := map[string]any{
		"items": []any{
			map[string]any{
				"clips_metadata": map[string]any{
					"music_info": map[string]any{
						"music_asset_info": map[string]any{
							"progressive_download_url":     "https://cdn.example/audio.m4a",
							"audio_asset_start_time_in_ms": float64(12345),
							"overlap_duration_in_ms":       float64(6789),
						},
					},
				},
			},
		},
	}

	got := extractInstagramMetadataAudioSelection(metadata)
	if got.URL != "https://cdn.example/audio.m4a" {
		t.Fatalf("URL = %q, want audio URL", got.URL)
	}
	if !got.HasStartOffset || got.StartOffset != 12345*time.Millisecond {
		t.Fatalf("StartOffset = %v has=%v, want 12.345s", got.StartOffset, got.HasStartOffset)
	}
	if !got.HasDuration || got.Duration != 6789*time.Millisecond {
		t.Fatalf("Duration = %v has=%v, want 6.789s", got.Duration, got.HasDuration)
	}
}

func TestInstagramAudioSelectionClampsAndFallsBackDuration(t *testing.T) {
	fallback := instagramAudioSelection{}
	if got := fallback.effectiveDuration(); got != 90*time.Second {
		t.Fatalf("fallback duration = %v, want 90s", got)
	}

	clamped := instagramAudioSelection{Duration: 120 * time.Second, HasDuration: true}
	if got := clamped.effectiveDuration(); got != 90*time.Second {
		t.Fatalf("clamped duration = %v, want 90s", got)
	}

	short := instagramAudioSelection{Duration: 17 * time.Second, HasDuration: true}
	if got := short.effectiveDuration(); got != 17*time.Second {
		t.Fatalf("short duration = %v, want 17s", got)
	}
}

func TestDownloaderScalesPhotoVideoDimensionsTo480WithoutCropping(t *testing.T) {
	tests := []struct {
		name       string
		width      int
		height     int
		wantWidth  int
		wantHeight int
	}{
		{name: "wide", width: 1280, height: 720, wantWidth: 480, wantHeight: 270},
		{name: "tall", width: 720, height: 1280, wantWidth: 270, wantHeight: 480},
		{name: "small", width: 640, height: 480, wantWidth: 480, wantHeight: 360},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWidth, gotHeight := scaledDimensions(tt.width, tt.height, photoVideoMaxDimension)
			if gotWidth != tt.wantWidth || gotHeight != tt.wantHeight {
				t.Fatalf("scaledDimensions() = %dx%d, want %dx%d", gotWidth, gotHeight, tt.wantWidth, tt.wantHeight)
			}
		})
	}
}

func TestDownloaderStripsInvalidBytesFromNetscapeCookieValues(t *testing.T) {
	path := writeCookiesFile(t, ".instagram.com", "sessionid", `\"quoted;cookie-value\"`)
	cookies, err := readNetscapeCookies(path, "www.instagram.com")
	if err != nil {
		t.Fatalf("readNetscapeCookies() error = %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if cookies[0].Value != "quotedcookie-value" {
		t.Fatalf("cookie value = %q, want quotedcookie-value", cookies[0].Value)
	}
}

func TestDownloaderExtractsPostImageInsteadOfProfileImageFromMetadata(t *testing.T) {
	metadata := map[string]any{
		"items": []any{
			map[string]any{
				"user": map[string]any{
					"profile_pic_url": "https://cdn.example/avatar.jpg",
				},
				"image_versions2": map[string]any{
					"candidates": []any{
						map[string]any{"url": "https://cdn.example/post-small.jpg", "width": float64(320), "height": float64(320)},
						map[string]any{"url": "https://cdn.example/post-large.jpg", "width": float64(1080), "height": float64(1080)},
					},
				},
			},
		},
	}

	got := extractInstagramMetadataImageURLs(metadata)
	if len(got) != 1 {
		t.Fatalf("image urls = %v, want one post image", got)
	}
	if got[0] != "https://cdn.example/post-large.jpg" {
		t.Fatalf("image url = %q, want post-large.jpg", got[0])
	}
}

func TestDownloaderReturnsOnlyVideoForCarouselWithAudio(t *testing.T) {
	ffmpeg := newFakeFFmpeg(t, "carousel-mp4")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/DXfJlTsDNFr/":
			_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="` + serverURL(r, "/first.jpg") + `"></head></html>`))
		case "/api/v1/media/3881863549996028267/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [{
					"carousel_media": [
						{"image_versions2": {"candidates": [{"url": "` + serverURL(r, "/first.jpg") + `"}]}},
						{"image_versions2": {"candidates": [{"url": "` + serverURL(r, "/second.jpg") + `"}]}}
					],
					"music_info": {"progressive_download_url": "` + serverURL(r, "/audio.m4a") + `"}
				}]
			}`))
		case "/first.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/second.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.m4a":
			w.Header().Set("Content-Type", "audio/mp4")
			_, _ = w.Write([]byte("audio"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{
		FFmpegPath:          ffmpeg.path,
		InstagramAPIBaseURL: server.URL,
		MaxBytes:            200_000,
	}.downloadInstagramPhotoPost(context.Background(), server.URL+"/p/DXfJlTsDNFr/")
	if err != nil {
		t.Fatalf("downloadInstagramPhotoPost() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Kind != MediaKindVideo {
		t.Fatalf("item[0].Kind = %q, want video", got[0].Kind)
	}
	if string(got[0].Bytes) != "carousel-mp4" {
		t.Fatalf("item[0].Bytes = %q, want carousel-mp4", got[0].Bytes)
	}
	if got[0].Duration != 90 {
		t.Fatalf("item[0].Duration = %f, want 90", got[0].Duration)
	}
	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if strings.Count(calls[0], "-loop 1") != 2 {
		t.Fatalf("ffmpeg args = %q, want two image inputs", calls[0])
	}
	if strings.Contains(calls[0], "xfade=transition=slideleft") {
		t.Fatalf("ffmpeg args = %q, did not expect xfade", calls[0])
	}
	if !strings.Contains(calls[0], "hstack=inputs=2") {
		t.Fatalf("ffmpeg args = %q, want two-image strip", calls[0])
	}
	if !strings.Contains(calls[0], "crop=w=") {
		t.Fatalf("ffmpeg args = %q, want moving crop viewport", calls[0])
	}
	if !strings.Contains(calls[0], "clip((t-3.500)/0.500\\,0\\,1)") {
		t.Fatalf("ffmpeg args = %q, want slide motion inside first segment", calls[0])
	}
	if !strings.Contains(calls[0], "-t 90") {
		t.Fatalf("ffmpeg args = %q, want 90s output", calls[0])
	}
	if !strings.Contains(calls[0], "-pix_fmt yuv420p") {
		t.Fatalf("ffmpeg args = %q, want yuv420p output", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 2, 6)
}

func TestDownloaderExtendsCarouselVideoWhenAudioIsShort(t *testing.T) {
	ffmpeg := newFakeFFmpeg(t, "short-carousel-mp4")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/DXfJlTsDNFr/":
			_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="` + serverURL(r, "/first.jpg") + `"></head></html>`))
		case "/api/v1/media/3881863549996028267/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [{
					"carousel_media": [
						{"image_versions2": {"candidates": [{"url": "` + serverURL(r, "/first.jpg") + `"}]}},
						{"image_versions2": {"candidates": [{"url": "` + serverURL(r, "/second.jpg") + `"}]}},
						{"image_versions2": {"candidates": [{"url": "` + serverURL(r, "/third.jpg") + `"}]}},
						{"image_versions2": {"candidates": [{"url": "` + serverURL(r, "/fourth.jpg") + `"}]}}
					],
					"music_info": {
						"music_asset_info": {
							"overlap_duration_in_ms": 5000,
							"progressive_download_url": "` + serverURL(r, "/audio.m4a") + `"
						}
					}
				}]
			}`))
		case "/first.jpg", "/second.jpg", "/third.jpg", "/fourth.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpegBytes(t, 1280, 720))
		case "/audio.m4a":
			w.Header().Set("Content-Type", "audio/mp4")
			_, _ = w.Write([]byte("audio"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := Downloader{
		FFmpegPath:          ffmpeg.path,
		InstagramAPIBaseURL: server.URL,
		MaxBytes:            200_000,
	}.downloadInstagramPhotoPost(context.Background(), server.URL+"/p/DXfJlTsDNFr/")
	if err != nil {
		t.Fatalf("downloadInstagramPhotoPost() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if got[0].Kind != MediaKindVideo {
		t.Fatalf("item[0].Kind = %q, want video", got[0].Kind)
	}
	if got[0].Duration != 16 {
		t.Fatalf("item[0].Duration = %f, want 16", got[0].Duration)
	}
	calls := ffmpeg.calls(t)
	if len(calls) != 1 {
		t.Fatalf("ffmpeg calls = %d, want 1", len(calls))
	}
	if strings.Count(calls[0], "-loop 1") != 4 {
		t.Fatalf("ffmpeg args = %q, want four image inputs", calls[0])
	}
	if strings.Contains(calls[0], "xfade=transition=slideleft") {
		t.Fatalf("ffmpeg args = %q, did not expect xfade", calls[0])
	}
	if !strings.Contains(calls[0], "hstack=inputs=4") {
		t.Fatalf("ffmpeg args = %q, want four-image strip", calls[0])
	}
	if !strings.Contains(calls[0], "clip((t-11.500)/0.500\\,0\\,1)") {
		t.Fatalf("ffmpeg args = %q, want final slide motion before last image", calls[0])
	}
	if !strings.Contains(calls[0], "atrim=start=0:duration=5") {
		t.Fatalf("ffmpeg args = %q, want 5s audio trim", calls[0])
	}
	if !strings.Contains(calls[0], "aresample=44100,aloop=loop=-1:size=220500,atrim=duration=16") {
		t.Fatalf("ffmpeg args = %q, want short audio looped to video duration", calls[0])
	}
	if !strings.Contains(calls[0], "-t 16") {
		t.Fatalf("ffmpeg args = %q, want 16s output", calls[0])
	}
	if !strings.Contains(calls[0], "-pix_fmt yuv420p") {
		t.Fatalf("ffmpeg args = %q, want yuv420p output", calls[0])
	}
	assertFastPhotoVideoEncoding(t, calls[0], 4, 6)
}

func TestDownloaderReturnsAudioUnavailableForMusicMetadataWithoutAudioURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/DXfJlTsDNFr/":
			_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="` + serverURL(r, "/image.jpg") + `"></head></html>`))
		case "/api/v1/media/3881863549996028267/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"music_info":{"music_asset_info":{"title":"song"}}}]}`))
		case "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpg"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := Downloader{
		InstagramAPIBaseURL: server.URL,
		MaxBytes:            1024,
	}.downloadInstagramPhotoPost(context.Background(), server.URL+"/p/DXfJlTsDNFr/")
	if !errors.Is(err, ErrInstagramAudioUnavailable) {
		t.Fatalf("downloadInstagramPhotoPost() error = %v, want %v", err, ErrInstagramAudioUnavailable)
	}
}

func TestDownloaderAppliesMaxBytesToSynthesizedVideo(t *testing.T) {
	ffmpeg := newFakeFFmpeg(t, "too-large")
	_, err := Downloader{FFmpegPath: ffmpeg.path, MaxBytes: 4}.synthesizeVideo(context.Background(), []byte("jpg"), []byte("audio"), 0, photoAudioMaxDuration)
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("synthesizeVideo() error = %v, want %v", err, ErrDownloadTooLarge)
	}
}

func assertFastPhotoVideoEncoding(t *testing.T, args string, imageInputs int, frameRate int) {
	t.Helper()
	framerateArg := fmt.Sprintf("-framerate %d", frameRate)
	if strings.Count(args, framerateArg) != imageInputs {
		t.Fatalf("ffmpeg args = %q, want %d image inputs at %d fps", args, imageInputs, frameRate)
	}
	outputRateArg := fmt.Sprintf("-r %d", frameRate)
	if !strings.Contains(args, outputRateArg) {
		t.Fatalf("ffmpeg args = %q, want %d fps output", args, frameRate)
	}
	if !strings.Contains(args, "-preset ultrafast") {
		t.Fatalf("ffmpeg args = %q, want ultrafast preset", args)
	}
	if !strings.Contains(args, "-tune stillimage") {
		t.Fatalf("ffmpeg args = %q, want stillimage tune", args)
	}
	if !strings.Contains(args, "-crf 32") {
		t.Fatalf("ffmpeg args = %q, want crf 32", args)
	}
}

type fakeYTDLP struct {
	path    string
	logPath string
}

type fakeFFmpeg struct {
	path    string
	logPath string
}

func newFakeFFmpeg(t *testing.T, output string) fakeFFmpeg {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg")
	logPath := filepath.Join(dir, "ffmpeg.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_FFMPEG_LOG"
out=""
for arg in "$@"; do
	out="$arg"
done
printf '%s' "$FAKE_FFMPEG_OUTPUT" > "$out"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	t.Setenv("FAKE_FFMPEG_OUTPUT", output)
	t.Setenv("FAKE_FFMPEG_LOG", logPath)
	return fakeFFmpeg{path: path, logPath: logPath}
}

func (f fakeFFmpeg) calls(t *testing.T) []string {
	t.Helper()

	body, err := os.ReadFile(f.logPath)
	if err != nil {
		t.Fatalf("read fake ffmpeg log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(body)), "\n")
}

func serverURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

func jpegBytes(t *testing.T, width int, height int) []byte {
	t.Helper()

	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func writeCookiesFile(t *testing.T, domain string, name string, value string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cookies.txt")
	body := "# Netscape HTTP Cookie File\n" + domain + "\tFALSE\t/\tFALSE\t0\t" + name + "\t" + value + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write cookies file: %v", err)
	}
	return path
}

func newFakeYTDLP(t *testing.T) fakeYTDLP {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "yt-dlp")
	logPath := filepath.Join(dir, "calls.log")

	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_YTDLP_LOG"

	for arg in "$@"; do
		if [ "$arg" = "-j" ]; then
			if [ -n "$FAKE_YTDLP_METADATA_FAIL" ]; then
				printf 'metadata-failed %05000d\n' 1 >&2
				exit 7
			fi
			if [ -n "$FAKE_YTDLP_METADATA_UNSUPPORTED_TIKTOK_PHOTO" ]; then
				printf 'ERROR: Unsupported URL: https://www.tiktok.com/@kittywxw5/photo/7633393932105288967?_r=1\n' >&2
				exit 1
			fi
			if [ -n "$FAKE_YTDLP_METADATA_NO_VIDEO" ]; then
				printf 'ERROR: [Instagram] DXfJlTsDNFr: There is no video in this post\n' >&2
				exit 1
		fi
		printf '{"title":"Test Reel","duration":12.5,"width":720,"height":1280}\n'
		exit 0
	fi
done

if [ -n "$FAKE_YTDLP_DOWNLOAD" ]; then
	printf '%s' "$FAKE_YTDLP_DOWNLOAD"
else
	printf 'mp4-bytes'
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	t.Setenv("FAKE_YTDLP_LOG", logPath)

	return fakeYTDLP{path: path, logPath: logPath}
}

func (f fakeYTDLP) calls(t *testing.T) []string {
	t.Helper()

	body, err := os.ReadFile(f.logPath)
	if err != nil {
		t.Fatalf("read fake yt-dlp log: %v", err)
	}

	return strings.Split(strings.TrimSpace(string(body)), "\n")
}

func (f fakeYTDLP) truncateCalls(t *testing.T) {
	t.Helper()

	if err := os.Truncate(f.logPath, 0); err != nil {
		t.Fatalf("truncate fake yt-dlp log: %v", err)
	}
}
