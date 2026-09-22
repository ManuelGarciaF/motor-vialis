package combinaciones_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
)

type fakeRepository struct {
	received combinaciones.Query
	calls    int
	result   []combinaciones.Combination
	total    int
	maximum  float64
	err      error
}

func (fake *fakeRepository) FindRanking(
	_ context.Context,
	query combinaciones.Query,
) ([]combinaciones.Combination, int, float64, error) {
	fake.received = query
	fake.calls++
	return fake.result, fake.total, fake.maximum, fake.err
}

func testPolicy() combinaciones.Policy {
	return combinaciones.Policy{
		DefaultPageSize:          10,
		MaximumPageSize:          50,
		WeakEvidenceAlternatives: 5,
	}
}

func hour(value int) *int { return &value }

func TestFindRankingRejectsAnHourOutsideTheDay(t *testing.T) {
	for _, value := range []int{-1, 24, 99} {
		repository := &fakeRepository{}
		service := combinaciones.NewService(repository, testPolicy())

		_, err := service.FindRanking(
			context.Background(),
			combinaciones.Request{Hour: hour(value)},
		)

		if !errors.Is(err, combinaciones.ErrHourOutOfRange) {
			t.Errorf("hour %d: error = %v, want ErrHourOutOfRange", value, err)
		}
		// Asking the database for an hour that cannot exist would answer an
		// impossible question with an empty page, which reads as "nobody
		// combines at that hour".
		if repository.calls != 0 {
			t.Errorf("hour %d: repository called %d times, want 0", value, repository.calls)
		}
	}
}

// An absent hour is the ranking's default view and a real answer, not a
// fallback: it must reach the repository as nil so it reads the whole-day row.
func TestFindRankingTreatsAnAbsentHourAsTheWholeDay(t *testing.T) {
	repository := &fakeRepository{}
	service := combinaciones.NewService(repository, testPolicy())

	page, err := service.FindRanking(context.Background(), combinaciones.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repository.received.Hour != nil {
		t.Errorf("repository received hour = %v, want nil", *repository.received.Hour)
	}
	if page.Hour != nil {
		t.Errorf("reported hour = %v, want nil", *page.Hour)
	}
}

func TestFindRankingAcceptsEveryHourOfTheDay(t *testing.T) {
	for value := 0; value < combinaciones.HoursInDay; value++ {
		repository := &fakeRepository{}
		service := combinaciones.NewService(repository, testPolicy())

		page, err := service.FindRanking(
			context.Background(),
			combinaciones.Request{Hour: hour(value)},
		)
		if err != nil {
			t.Fatalf("hour %d: unexpected error: %v", value, err)
		}
		if page.Hour == nil || *page.Hour != value {
			t.Errorf("hour %d: reported %v", value, page.Hour)
		}
	}
}

func TestFindRankingClampsThePageToThePolicy(t *testing.T) {
	testCases := map[string]struct {
		requested int
		want      int
	}{
		"omitted uses the default":  {requested: 0, want: 10},
		"negative uses the default": {requested: -5, want: 10},
		"within bounds is kept":     {requested: 25, want: 25},
		"above the maximum is cut":  {requested: 999, want: 50},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := combinaciones.NewService(repository, testPolicy())

			page, err := service.FindRanking(
				context.Background(),
				combinaciones.Request{Limit: testCase.requested},
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if page.Limit != testCase.want {
				t.Errorf("reported limit = %d, want %d", page.Limit, testCase.want)
			}
			if repository.received.Limit != testCase.want {
				t.Errorf(
					"repository received limit = %d, want %d",
					repository.received.Limit,
					testCase.want,
				)
			}
		})
	}
}

func TestFindRankingFloorsANegativeOffset(t *testing.T) {
	repository := &fakeRepository{}
	service := combinaciones.NewService(repository, testPolicy())

	page, err := service.FindRanking(
		context.Background(),
		combinaciones.Request{Offset: -10},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repository.received.Offset != 0 || page.Offset != 0 {
		t.Errorf(
			"offset = %d (reported %d), want 0",
			repository.received.Offset,
			page.Offset,
		)
	}
}

// The threshold decides when the engine stops asserting. It lives in the
// policy so every client agrees on it and changing it is a change to the
// engine, not to whichever screen renders the row.
func TestFindRankingMarksEstimatesSpreadTooThin(t *testing.T) {
	testCases := map[string]struct {
		alternatives float64
		want         bool
	}{
		"a forced combination is not weak": {alternatives: 1, want: false},
		"just under the threshold":         {alternatives: 4.9, want: false},
		"exactly at the threshold":         {alternatives: 5, want: false},
		"above the threshold":              {alternatives: 5.1, want: true},
		"at the ETL ceiling":               {alternatives: 10, want: true},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			repository := &fakeRepository{result: []combinaciones.Combination{
				{AverageAlternatives: testCase.alternatives},
			}}
			service := combinaciones.NewService(repository, testPolicy())

			page, err := service.FindRanking(
				context.Background(),
				combinaciones.Request{},
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if page.Combinations[0].WeakEvidence != testCase.want {
				t.Errorf(
					"weakEvidence = %v for %v alternatives, want %v",
					page.Combinations[0].WeakEvidence,
					testCase.alternatives,
					testCase.want,
				)
			}
		})
	}
}

func TestFindRankingSortsByEstimatedTrips(t *testing.T) {
	repository := &fakeRepository{result: []combinaciones.Combination{
		{First: combinaciones.Leg{LineID: 3}, Second: combinaciones.Leg{LineID: 9}, EstimatedTrips: 10},
		{First: combinaciones.Leg{LineID: 1}, Second: combinaciones.Leg{LineID: 2}, EstimatedTrips: 90},
		{First: combinaciones.Leg{LineID: 1}, Second: combinaciones.Leg{LineID: 5}, EstimatedTrips: 10},
	}}
	service := combinaciones.NewService(repository, testPolicy())

	page, err := service.FindRanking(context.Background(), combinaciones.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Largest first, then a deterministic walk of the two line ids so equal
	// estimates keep the same order across runs. A flow split among several
	// combinations gives all of them the same share, so ties are common.
	want := [][2]int64{{1, 2}, {1, 5}, {3, 9}}
	for index, pair := range want {
		got := page.Combinations[index]
		if got.First.LineID != pair[0] || got.Second.LineID != pair[1] {
			t.Errorf(
				"position %d = (%d, %d), want (%d, %d)",
				index, got.First.LineID, got.Second.LineID, pair[0], pair[1],
			)
		}
	}
}

// The maximum is the whole ranking's, not the page's: reading it off the page
// would make the second page paint its severity against its own first row.
func TestFindRankingReportsTheTotalAndMaximumFromTheRepository(t *testing.T) {
	repository := &fakeRepository{
		result:  []combinaciones.Combination{{EstimatedTrips: 40}},
		total:   873,
		maximum: 9100,
	}
	service := combinaciones.NewService(repository, testPolicy())

	page, err := service.FindRanking(
		context.Background(),
		combinaciones.Request{Offset: 100},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if page.Total != 873 {
		t.Errorf("total = %d, want 873", page.Total)
	}
	if page.MaximumEstimatedTrips != 9100 {
		t.Errorf("maximum = %v, want 9100", page.MaximumEstimatedTrips)
	}
}

func TestFindRankingReportsAnEmptyResultAsAnEmptyList(t *testing.T) {
	repository := &fakeRepository{result: nil}
	service := combinaciones.NewService(repository, testPolicy())

	page, err := service.FindRanking(context.Background(), combinaciones.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Una hora en la que ninguna combinación sobrevive al filtro de línea
	// directa es una respuesta real y útil, así que tiene que serializar como
	// lista vacía y no como null.
	if page.Combinations == nil {
		t.Error("combinations is nil, want an empty slice")
	}
}

func TestFindRankingWrapsARepositoryFailure(t *testing.T) {
	failure := errors.New("database is down")
	repository := &fakeRepository{err: failure}
	service := combinaciones.NewService(repository, testPolicy())

	_, err := service.FindRanking(context.Background(), combinaciones.Request{})

	if !errors.Is(err, failure) {
		t.Errorf("error = %v, want it to wrap %v", err, failure)
	}
}
