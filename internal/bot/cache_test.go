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
