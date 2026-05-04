package metrics

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type CacheStats struct {
	Entries  int
	Inflight int
	Waiters  int
}

type Options struct {
	RegisteredUsers func() int
	CacheStats      func() CacheStats
}

type Recorder struct {
	registry *prometheus.Registry

	startCommandsTotal *prometheus.CounterVec
	inlineQueriesTotal *prometheus.CounterVec
	inlineChosenTotal  *prometheus.CounterVec
	downloadsTotal     *prometheus.CounterVec
	downloadDuration   *prometheus.HistogramVec
	downloadedMedia    *prometheus.CounterVec
	downloadedSize     *prometheus.HistogramVec
	uploadsTotal       *prometheus.CounterVec
	uploadDuration     *prometheus.HistogramVec
	uploadSize         *prometheus.HistogramVec
	inlineMediaSent    *prometheus.CounterVec
	ffmpegRunsTotal    *prometheus.CounterVec
	ffmpegDuration     *prometheus.HistogramVec
}

func New(opts Options) *Recorder {
	registry := prometheus.NewRegistry()
	recorder := &Recorder{
		registry: registry,
		startCommandsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reelsovoz_start_commands_total",
			Help: "Total /start commands handled by result.",
		}, []string{"result"}),
		inlineQueriesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reelsovoz_inline_queries_total",
			Help: "Total inline queries handled by result and source platform.",
		}, []string{"result", "platform"}),
		inlineChosenTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reelsovoz_inline_chosen_total",
			Help: "Total chosen inline results handled by result, media kind, source, and source platform.",
		}, []string{"result", "kind", "source", "platform"}),
		downloadsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reelsovoz_downloads_total",
			Help: "Total media download operations by platform and status.",
		}, []string{"platform", "status"}),
		downloadDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "reelsovoz_download_duration_seconds",
			Help:    "Media download duration in seconds by platform and status.",
			Buckets: []float64{1, 2.5, 5, 10, 30, 60, 120, 180, 300},
		}, []string{"platform", "status"}),
		downloadedMedia: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reelsovoz_downloaded_media_total",
			Help: "Total media items returned by downloads by platform and kind.",
		}, []string{"platform", "kind"}),
		downloadedSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "reelsovoz_downloaded_media_size_bytes",
			Help:    "Downloaded media item size in bytes by platform and kind.",
			Buckets: sizeBuckets(),
		}, []string{"platform", "kind"}),
		uploadsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reelsovoz_storage_uploads_total",
			Help: "Total storage chat uploads by media kind and status.",
		}, []string{"kind", "status"}),
		uploadDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "reelsovoz_storage_upload_duration_seconds",
			Help:    "Storage chat upload duration in seconds by media kind and status.",
			Buckets: []float64{1, 2.5, 5, 10, 30, 60, 120, 180, 300},
		}, []string{"kind", "status"}),
		uploadSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "reelsovoz_storage_upload_size_bytes",
			Help:    "Storage chat upload payload size in bytes by media kind.",
			Buckets: sizeBuckets(),
		}, []string{"kind"}),
		inlineMediaSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reelsovoz_inline_media_sent_total",
			Help: "Total inline messages edited to media by media kind, source, and status.",
		}, []string{"kind", "source", "status"}),
		ffmpegRunsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "reelsovoz_ffmpeg_runs_total",
			Help: "Total FFmpeg executions by platform, operation, and status.",
		}, []string{"platform", "operation", "status"}),
		ffmpegDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "reelsovoz_ffmpeg_duration_seconds",
			Help:    "Wall-clock time spent inside FFmpeg process execution.",
			Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 180},
		}, []string{"platform", "operation", "status"}),
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewBuildInfoCollector(),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "reelsovoz_registered_users",
			Help: "Current number of registered Telegram user storage chats.",
		}, func() float64 {
			if opts.RegisteredUsers == nil {
				return 0
			}
			return float64(opts.RegisteredUsers())
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "reelsovoz_media_cache_entries",
			Help: "Current number of cached media entries.",
		}, func() float64 {
			if opts.CacheStats == nil {
				return 0
			}
			return float64(opts.CacheStats().Entries)
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "reelsovoz_media_cache_inflight",
			Help: "Current number of media cache preparations in progress.",
		}, func() float64 {
			if opts.CacheStats == nil {
				return 0
			}
			return float64(opts.CacheStats().Inflight)
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "reelsovoz_media_cache_waiters",
			Help: "Current number of pending inline messages waiting for prepared media.",
		}, func() float64 {
			if opts.CacheStats == nil {
				return 0
			}
			return float64(opts.CacheStats().Waiters)
		}),
		recorder.startCommandsTotal,
		recorder.inlineQueriesTotal,
		recorder.inlineChosenTotal,
		recorder.downloadsTotal,
		recorder.downloadDuration,
		recorder.downloadedMedia,
		recorder.downloadedSize,
		recorder.uploadsTotal,
		recorder.uploadDuration,
		recorder.uploadSize,
		recorder.inlineMediaSent,
		recorder.ffmpegRunsTotal,
		recorder.ffmpegDuration,
	)
	return recorder
}

func (r *Recorder) Handler() http.Handler {
	if r == nil {
		r = New(Options{})
	}
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{
		Registry:          r.registry,
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.ContinueOnError,
		ErrorLog:          log.Default(),
	})
}

func Start(ctx context.Context, addr string, logger *slog.Logger, handler http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", handler)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		if err := server.Shutdown(context.Background()); err != nil && logger != nil {
			logger.Error("metrics server shutdown failed", "error", err)
		}
	}()

	go func() {
		if logger != nil {
			logger.Info("metrics server started", "addr", addr)
		}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed && logger != nil {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	return server
}

func (r *Recorder) IncStartCommand(result string) {
	if r == nil {
		return
	}
	r.startCommandsTotal.WithLabelValues(label(result)).Inc()
}

func (r *Recorder) IncInlineQuery(result string, platform string) {
	if r == nil {
		return
	}
	r.inlineQueriesTotal.WithLabelValues(label(result), platformLabel(platform)).Inc()
}

func (r *Recorder) IncInlineChosen(result string, kind string, source string, platform string) {
	if r == nil {
		return
	}
	r.inlineChosenTotal.WithLabelValues(label(result), kindLabel(kind), label(source), platformLabel(platform)).Inc()
}

func (r *Recorder) ObserveDownload(platform string, status string, duration time.Duration) {
	if r == nil {
		return
	}
	r.downloadsTotal.WithLabelValues(platformLabel(platform), statusLabel(status)).Inc()
	r.downloadDuration.WithLabelValues(platformLabel(platform), statusLabel(status)).Observe(duration.Seconds())
}

func (r *Recorder) IncDownloadedMedia(platform string, kind string) {
	if r == nil {
		return
	}
	r.downloadedMedia.WithLabelValues(platformLabel(platform), kindLabel(kind)).Inc()
}

func (r *Recorder) ObserveDownloadedMediaSize(platform string, kind string, sizeBytes int) {
	if r == nil {
		return
	}
	r.downloadedSize.WithLabelValues(platformLabel(platform), kindLabel(kind)).Observe(float64(sizeBytes))
}

func (r *Recorder) ObserveStorageUpload(kind string, status string, duration time.Duration) {
	if r == nil {
		return
	}
	r.uploadsTotal.WithLabelValues(kindLabel(kind), statusLabel(status)).Inc()
	r.uploadDuration.WithLabelValues(kindLabel(kind), statusLabel(status)).Observe(duration.Seconds())
}

func (r *Recorder) ObserveStorageUploadSize(kind string, sizeBytes int) {
	if r == nil {
		return
	}
	r.uploadSize.WithLabelValues(kindLabel(kind)).Observe(float64(sizeBytes))
}

func (r *Recorder) IncInlineMediaSent(kind string, source string, status string) {
	if r == nil {
		return
	}
	r.inlineMediaSent.WithLabelValues(kindLabel(kind), label(source), statusLabel(status)).Inc()
}

func (r *Recorder) ObserveFFmpeg(platform string, operation string, status string, duration time.Duration) {
	if r == nil {
		return
	}
	r.ffmpegRunsTotal.WithLabelValues(platformLabel(platform), label(operation), statusLabel(status)).Inc()
	r.ffmpegDuration.WithLabelValues(platformLabel(platform), label(operation), statusLabel(status)).Observe(duration.Seconds())
}

func sizeBuckets() []float64 {
	return []float64{
		128 * 1024,
		256 * 1024,
		512 * 1024,
		1024 * 1024,
		2 * 1024 * 1024,
		4 * 1024 * 1024,
		8 * 1024 * 1024,
		16 * 1024 * 1024,
		32 * 1024 * 1024,
		64 * 1024 * 1024,
	}
}

func platformLabel(value string) string {
	switch value {
	case "instagram", "tiktok":
		return value
	default:
		return "unknown"
	}
}

func kindLabel(value string) string {
	switch value {
	case "video", "photo":
		return value
	default:
		return "unknown"
	}
}

func statusLabel(value string) string {
	switch value {
	case "success", "error", "timeout":
		return value
	default:
		return "unknown"
	}
}

func label(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
