package bunching

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// Fingerprint compares complete provider snapshots, including unmapped/stale
// vehicles. Raw insert counts cannot detect novel responses when only unmapped
// vehicles changed. Coordinates changing without timestamps are not new evidence.
func Fingerprint(positions []Position) string {
	latest := make(map[string]Position)
	for _, p := range positions {
		old, ok := latest[p.VehicleID]
		if !ok || p.ObservedAt.After(old.ObservedAt) || p.ObservedAt.Equal(old.ObservedAt) && (p.ProviderRouteID < old.ProviderRouteID || p.ProviderRouteID == old.ProviderRouteID && p.ProviderDirection < old.ProviderDirection) {
			latest[p.VehicleID] = p
		}
	}
	type item struct {
		ID, Timestamp, Route string
		Direction            int16
	}
	items := make([]item, 0, len(latest))
	for id, position := range latest {
		items = append(items, item{id, position.ObservedAt.UTC().Format(time.RFC3339Nano), position.ProviderRouteID, position.ProviderDirection})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	encoded, _ := json.Marshal(items) // These string/integer-only structs cannot fail.
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
