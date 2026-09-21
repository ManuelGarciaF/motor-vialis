package tomtom

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTileCacheExpiresAndEvictsByByteCapacity(t *testing.T) {
	cache := newTileCache(2, 5, 30*time.Minute)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	load := func(key cacheKey, data string, at time.Time) (bool, error) {
		_, hit, err := cache.load(context.Background(), key, at, func() (cacheEntry, error) {
			return cacheEntry{key: key, data: []byte(data), fetchedAt: at}, nil
		})
		return hit, err
	}
	first := cacheKey{Style: flowStyle, Tile: Tile{Zoom: 14, X: 1, Y: 1}}
	second := cacheKey{Style: flowStyle, Tile: Tile{Zoom: 14, X: 2, Y: 1}}
	if hit, err := load(first, "123", now); err != nil || hit {
		t.Fatalf("first load hit=%v err=%v", hit, err)
	}
	if hit, err := load(second, "456", now); err != nil || hit {
		t.Fatalf("second load hit=%v err=%v", hit, err)
	}
	if _, found := cache.getLockedForTest(first, now); found {
		t.Fatal("first entry was not evicted when cache exceeded byte capacity")
	}
	if _, found := cache.getLockedForTest(second, now.Add(31*time.Minute)); found {
		t.Fatal("second entry remained after TTL")
	}
}

func TestTileCacheDeduplicatesConcurrentLoads(t *testing.T) {
	cache := newTileCache(10, 1024, 30*time.Minute)
	key := cacheKey{Style: flowStyle, Tile: Tile{Zoom: 14, X: 1, Y: 1}}
	now := time.Now()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	loader := func() (cacheEntry, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return cacheEntry{key: key, data: []byte("tile"), fetchedAt: now}, nil
	}

	var wait sync.WaitGroup
	wait.Add(2)
	hits := make([]bool, 2)
	for index := range hits {
		go func() {
			defer wait.Done()
			_, hits[index], _ = cache.load(context.Background(), key, now, loader)
		}()
	}
	<-started
	close(release)
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", calls.Load())
	}
	if hits[0] == hits[1] {
		t.Fatalf("cache hits = %v, want one leader and one reused result", hits)
	}
}

func (cache *tileCache) getLockedForTest(key cacheKey, now time.Time) (cacheEntry, bool) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	return cache.getLocked(key, now)
}
