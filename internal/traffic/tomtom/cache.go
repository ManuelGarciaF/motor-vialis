package tomtom

import (
	"container/list"
	"context"
	"sync"
	"time"
)

type cacheKey struct {
	Style string
	Tile  Tile
}

type cacheEntry struct {
	key       cacheKey
	data      []byte
	fetchedAt time.Time
	expires   time.Time
}

type cacheCall struct {
	done  chan struct{}
	entry cacheEntry
	err   error
}

type tileCache struct {
	mutex        sync.Mutex
	entries      map[cacheKey]*list.Element
	flights      map[cacheKey]*cacheCall
	recent       *list.List
	maximum      int
	maximumBytes int
	usedBytes    int
	ttl          time.Duration
}

func newTileCache(maximum, maximumBytes int, ttl time.Duration) *tileCache {
	return &tileCache{
		entries:      make(map[cacheKey]*list.Element),
		flights:      make(map[cacheKey]*cacheCall),
		recent:       list.New(),
		maximum:      maximum,
		maximumBytes: maximumBytes,
		ttl:          ttl,
	}
}

func (cache *tileCache) load(
	ctx context.Context,
	key cacheKey,
	now time.Time,
	fetch func() (cacheEntry, error),
) (cacheEntry, bool, error) {
	cache.mutex.Lock()
	if entry, found := cache.getLocked(key, now); found {
		cache.mutex.Unlock()
		return entry, true, nil
	}
	if call, found := cache.flights[key]; found {
		cache.mutex.Unlock()
		select {
		case <-ctx.Done():
			return cacheEntry{}, false, ctx.Err()
		case <-call.done:
			return call.entry, true, call.err
		}
	}
	call := &cacheCall{done: make(chan struct{})}
	cache.flights[key] = call
	cache.mutex.Unlock()

	entry, err := fetch()

	cache.mutex.Lock()
	if err == nil {
		cache.addLocked(entry)
	}
	call.entry = entry
	call.err = err
	delete(cache.flights, key)
	close(call.done)
	cache.mutex.Unlock()
	return entry, false, err
}

func (cache *tileCache) getLocked(key cacheKey, now time.Time) (cacheEntry, bool) {
	element, found := cache.entries[key]
	if !found {
		return cacheEntry{}, false
	}
	entry := element.Value.(cacheEntry)
	if now.Sub(entry.fetchedAt) > cache.ttl ||
		(!entry.expires.IsZero() && !now.Before(entry.expires)) {
		cache.removeLocked(element)
		return cacheEntry{}, false
	}
	cache.recent.MoveToFront(element)
	return entry, true
}

func (cache *tileCache) addLocked(entry cacheEntry) {
	if cache.maximum <= 0 || cache.maximumBytes <= 0 ||
		len(entry.data) > cache.maximumBytes {
		return
	}
	if current, found := cache.entries[entry.key]; found {
		cache.removeLocked(current)
	}
	element := cache.recent.PushFront(entry)
	cache.entries[entry.key] = element
	cache.usedBytes += len(entry.data)
	for cache.recent.Len() > cache.maximum || cache.usedBytes > cache.maximumBytes {
		cache.removeLocked(cache.recent.Back())
	}
}

func (cache *tileCache) invalidate(key cacheKey) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if element, found := cache.entries[key]; found {
		cache.removeLocked(element)
	}
}

func (cache *tileCache) removeLocked(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(cacheEntry)
	delete(cache.entries, entry.key)
	cache.usedBytes -= len(entry.data)
	cache.recent.Remove(element)
}
