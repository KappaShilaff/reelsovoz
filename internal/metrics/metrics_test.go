package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerExposesApplicationMetrics(t *testing.T) {
	recorder := New(Options{
		RegisteredUsers: func() int { return 2 },
		CacheStats: func() CacheStats {
			return CacheStats{Entries: 3, Inflight: 1, Waiters: 4}
		},
	})
	recorder.IncStartCommand("success")
	recorder.IncInlineQuery("cache_hit", "instagram")
	recorder.IncInlineChosen("cache_hit", "video", "cache", "instagram")
	recorder.ObserveDownload("instagram", "success", 2*time.Second)
	recorder.IncDownloadedMedia("instagram", "video")
	recorder.ObserveDownloadedMediaSize("instagram", "video", 1024)
	recorder.ObserveStorageUpload("video", "success", time.Second)
	recorder.ObserveStorageUploadSize("video", 1024)
	recorder.IncInlineMediaSent("video", "async", "success")
	recorder.ObserveFFmpeg("instagram", "single_photo_audio", "success", time.Second)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	recorder.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, want := range []string{
		"reelsovoz_registered_users 2",
		"reelsovoz_media_cache_entries 3",
		"reelsovoz_media_cache_inflight 1",
		"reelsovoz_media_cache_waiters 4",
		"reelsovoz_start_commands_total{result=\"success\"} 1",
		"reelsovoz_inline_queries_total{platform=\"instagram\",result=\"cache_hit\"} 1",
		"reelsovoz_downloads_total{platform=\"instagram\",status=\"success\"} 1",
		"reelsovoz_downloaded_media_total{kind=\"video\",platform=\"instagram\"} 1",
		"reelsovoz_storage_uploads_total{kind=\"video\",status=\"success\"} 1",
		"reelsovoz_inline_media_sent_total{kind=\"video\",source=\"async\",status=\"success\"} 1",
		"reelsovoz_ffmpeg_runs_total{operation=\"single_photo_audio\",platform=\"instagram\",status=\"success\"} 1",
		"go_goroutines",
		"process_cpu_seconds_total",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}
