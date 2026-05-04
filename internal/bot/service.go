package bot

import (
	"context"
	"fmt"

	"github.com/KappaShilaff/reelsovoz/internal/reels"
)

type MediaKind string

const (
	MediaKindVideo MediaKind = "video"
	MediaKindPhoto MediaKind = "photo"
)

type Media struct {
	Kind     MediaKind
	Filename string
	Bytes    []byte
	Caption  string
	Duration int64
	Width    int64
	Height   int64
}

type ReelsService interface {
	Download(ctx context.Context, reelURL string) ([]Media, error)
}

type YTDLPService struct {
	Downloader reels.Downloader
}

func (s YTDLPService) Download(ctx context.Context, reelURL string) ([]Media, error) {
	downloaded, err := s.Downloader.Download(ctx, reelURL)
	if err != nil {
		return nil, fmt.Errorf("download reel video: %w", err)
	}
	media := make([]Media, 0, len(downloaded))
	for _, item := range downloaded {
		media = append(media, Media{
			Kind:     mediaKind(item.Kind),
			Filename: item.Filename,
			Bytes:    item.Bytes,
			Duration: int64(item.Duration),
			Width:    int64(item.Width),
			Height:   int64(item.Height),
		})
	}
	return media, nil
}

func mediaKind(kind reels.MediaKind) MediaKind {
	switch kind {
	case reels.MediaKindPhoto:
		return MediaKindPhoto
	case reels.MediaKindVideo:
		return MediaKindVideo
	default:
		return MediaKind(kind)
	}
}
