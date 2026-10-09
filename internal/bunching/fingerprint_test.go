package bunching

import (
	"testing"
	"time"
)

func TestFingerprintIsTimestampBasedAndOrderIndependent(t *testing.T) {
	now := time.Now()
	a := Position{VehicleID: "a", ObservedAt: now}
	b := Position{VehicleID: "unmapped", ObservedAt: now}
	first := Fingerprint([]Position{a, b})
	a.Longitude = 70
	if Fingerprint([]Position{b, a, a}) != first {
		t.Fatal("order, duplicates or unchanged-timestamp coordinates changed fingerprint")
	}
	b.ObservedAt = now.Add(time.Second)
	if Fingerprint([]Position{a, b}) == first {
		t.Fatal("unmapped timestamp advancement ignored")
	}
}
