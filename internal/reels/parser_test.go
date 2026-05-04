package reels

import (
	"errors"
	"testing"
)

func TestExtractURLSupportsTikTok(t *testing.T) {
	tests := []string{
		"https://www.tiktok.com/@user/video/123",
		"https://tiktok.com/@user/video/123",
		"https://vm.tiktok.com/ZMabc/",
		"https://vt.tiktok.com/ZMabc/",
		"vm.tiktok.com/ZMabc/",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			got, err := ExtractURL("look " + tt)
			if err != nil {
				t.Fatalf("ExtractURL() error = %v", err)
			}
			if got != tt {
				t.Fatalf("ExtractURL() = %q, want %q", got, tt)
			}
		})
	}
}

func TestExtractURLSupportsInstagramPaths(t *testing.T) {
	tests := []string{
		"https://www.instagram.com/reel/ABC/",
		"https://instagram.com/reels/ABC/",
		"https://instagram.com/p/ABC/?img_index=1",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			got, err := ExtractURL("look " + tt)
			if err != nil {
				t.Fatalf("ExtractURL() error = %v", err)
			}
			if got != tt {
				t.Fatalf("ExtractURL() = %q, want %q", got, tt)
			}
		})
	}
}

func TestExtractURLTrimsTrailingPunctuation(t *testing.T) {
	got, err := ExtractURL("send https://instagram.com/reel/ABC/.")
	if err != nil {
		t.Fatalf("ExtractURL() error = %v", err)
	}
	if got != "https://instagram.com/reel/ABC/" {
		t.Fatalf("ExtractURL() = %q", got)
	}
}

func TestExtractURLRejectsNoURL(t *testing.T) {
	_, err := ExtractURL("no links here")
	if !errors.Is(err, ErrNoURL) {
		t.Fatalf("ExtractURL() error = %v, want %v", err, ErrNoURL)
	}
}

func TestExtractURLRejectsUnsupportedURL(t *testing.T) {
	tests := []string{
		"https://youtube.com/shorts/abc",
		"https://instagram.com/stories/abc",
		"https://example.com/reel/abc",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			_, err := ExtractURL(tt)
			if !errors.Is(err, ErrUnsupportedURL) {
				t.Fatalf("ExtractURL() error = %v, want %v", err, ErrUnsupportedURL)
			}
		})
	}
}

func TestExtractURLRejectsMultipleURLs(t *testing.T) {
	tests := []string{
		"https://instagram.com/reel/a https://instagram.com/reel/b",
		"https://instagram.com/reel/a https://example.com",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			_, err := ExtractURL(tt)
			if !errors.Is(err, ErrMultipleURLs) {
				t.Fatalf("ExtractURL() error = %v, want %v", err, ErrMultipleURLs)
			}
		})
	}
}
