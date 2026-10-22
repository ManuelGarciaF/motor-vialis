package bottlenecks

import (
	"fmt"
	"math"
)

// Sector is a longitudinal interval on a directed shape, with a zero-based index.
// Geometry extraction is the repository's responsibility, using these same
// progress distances. Start/end belong to this layout and its shape version.
type Sector struct {
	Index                  int
	StartMeters, EndMeters float64
}

func (s Sector) LengthMeters() float64 { return s.EndMeters - s.StartMeters }

// Layout is immutable after construction. Nominal length is part of its identity:
// sector 3 in a 300 m layout is not sector 3 in a 500 m layout.
type Layout struct {
	version ShapeVersion
	length  float64
	nominal float64
	sectors []Sector
}

// NewLayout splits measured geometry length into nominal sectors. A final
// remainder shorter than half the nominal length is merged with its predecessor.
// A shorter-than-nominal route remains a single sector. The 100,000-sector
// allocation ceiling is a defensive resource limit, not a model parameter.
func NewLayout(version ShapeVersion, lengthMeters, nominalMeters float64) (Layout, error) {
	if err := version.validate(); err != nil {
		return Layout{}, err
	}
	if !finite(lengthMeters) || !finite(nominalMeters) || lengthMeters <= 0 || nominalMeters <= 0 || lengthMeters > 1e9 || nominalMeters > 1e9 {
		return Layout{}, fmt.Errorf("invalid segmentation lengths")
	}

	length, nominal := meters(lengthMeters), meters(nominalMeters)
	if length <= 0 || nominal <= 0 || length/nominal > 100000 {
		return Layout{}, fmt.Errorf("segmentation precision or allocation limit exceeded")
	}

	count := int(math.Floor(length / nominal))
	remainder := meters(length - float64(count)*nominal)
	if remainder >= nominal/2 {
		count++
	}
	count = max(1, count)
	sectors := make([]Sector, count)
	for i := range sectors {
		end := meters(float64(i+1) * nominal)
		if i == count-1 {
			end = length
		}
		sectors[i] = Sector{Index: i, StartMeters: meters(float64(i) * nominal), EndMeters: end}
	}

	return Layout{version: version, length: length, nominal: nominal, sectors: sectors}, nil
}

func (l Layout) Version() ShapeVersion  { return l.version }
func (l Layout) LengthMeters() float64  { return l.length }
func (l Layout) NominalMeters() float64 { return l.nominal }
func (l Layout) Sectors() []Sector      { return append([]Sector(nil), l.sectors...) }
