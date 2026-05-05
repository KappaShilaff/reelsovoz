package bot

import "testing"

func TestMediaCacheStats(t *testing.T) {
	cache := NewMediaCache(defaultMediaCacheTTL)
	cache.Set("cached", []CachedMedia{{Kind: MediaKindVideo, FileID: "file-id"}})
	if !cache.StartPrepare("inflight") {
		t.Fatal("StartPrepare() = false, want true")
	}
	cache.AddWaiter("inflight", "inline-message-1")
	cache.AddWaiter("inflight", "inline-message-2")

	stats := cache.Stats()
	if stats.Entries != 1 || stats.Inflight != 1 || stats.Waiters != 2 {
		t.Fatalf("stats = %#v, want entries=1 inflight=1 waiters=2", stats)
	}
}

func TestMediaCacheDoesNotStorePhotos(t *testing.T) {
	cache := NewMediaCache(defaultMediaCacheTTL)
	cache.Set("photo-only", []CachedMedia{{Kind: MediaKindPhoto, FileID: "photo-file-id"}})
	if _, ok := cache.Get("photo-only"); ok {
		t.Fatal("photo-only cache entry found, want miss")
	}

	cache.Set("mixed", []CachedMedia{
		{Kind: MediaKindPhoto, FileID: "photo-file-id"},
		{Kind: MediaKindVideo, FileID: "video-file-id"},
	})
	got, ok := cache.Get("mixed")
	if !ok {
		t.Fatal("mixed cache entry not found")
	}
	if len(got) != 1 || got[0].Kind != MediaKindVideo || got[0].FileID != "video-file-id" {
		t.Fatalf("cached media = %#v, want only video", got)
	}
}
