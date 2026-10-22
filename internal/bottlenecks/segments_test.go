package bottlenecks

import (
	"math"
	"reflect"
	"testing"
)

func testVersion() ShapeVersion {
	return ShapeVersion{RouteID: "r", Direction: 0, ShapeID: "s", ShapeHash: "hash"}
}

func TestLayout(t *testing.T) {
	for _, tc := range []struct {
		name            string
		length, nominal float64
		ends            []float64
	}{
		{"uniform", 2000, 500, []float64{500, 1000, 1500, 2000}},
		{"merge short remainder", 2100, 500, []float64{500, 1000, 1500, 2100}},
		{"half remainder", 2250, 500, []float64{500, 1000, 1500, 2000, 2250}},
		{"roundoff at half remainder", 1999.9999999999, 800, []float64{800, 1600, 2000}},
		{"short shape", 200, 500, []float64{200}},
		{"decimal nominal", 1, 0.3, []float64{0.3, 0.6, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layout, err := NewLayout(testVersion(), tc.length, tc.nominal)
			if err != nil {
				t.Fatal(err)
			}
			sectors := layout.Sectors()
			var ends []float64
			previous := 0.0
			for i, s := range sectors {
				if s.Index != i || s.StartMeters != previous || s.LengthMeters() <= 0 {
					t.Fatalf("invalid sector: %+v", s)
				}
				ends = append(ends, s.EndMeters)
				previous = s.EndMeters
			}

			if !reflect.DeepEqual(ends, tc.ends) {
				t.Fatalf("ends %v, want %v", ends, tc.ends)
			}
			sectors[0].EndMeters = -1
			if layout.Sectors()[0].EndMeters < 0 {
				t.Fatal("layout mutated by accessor")
			}
			if layout.Version() != testVersion() || layout.NominalMeters() != meters(tc.nominal) || layout.LengthMeters() != meters(tc.length) {
				t.Fatal("layout metadata mismatch")
			}
		})
	}
}

func TestLayoutRejectsInvalidInput(t *testing.T) {
	for _, values := range [][2]float64{{0, 500}, {-1, 500}, {1000, 0}, {math.NaN(), 500}, {1000, math.Inf(1)}, {1e10, 500}, {1000, 0.0000001}, {1000, 0.001}} {
		if _, err := NewLayout(testVersion(), values[0], values[1]); err == nil {
			t.Fatalf("accepted %v", values)
		}
	}

	for _, v := range []ShapeVersion{{}, {RouteID: "r", ShapeID: "s", ShapeHash: "h", Direction: 2}, {RouteID: " ", ShapeID: "s", ShapeHash: "h"}} {
		if _, err := NewLayout(v, 1000, 500); err == nil {
			t.Fatalf("accepted version %+v", v)
		}
	}
}
