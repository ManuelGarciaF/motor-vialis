package route

import "testing"

func TestValidationErrorRootedReplacesTheRoutePrefix(t *testing.T) {
	err := &ValidationError{Field: "route.stops[3].id", Message: "must be unique"}

	rooted := err.Rooted("baseline")

	if rooted.Field != "baseline.stops[3].id" {
		t.Fatalf("field = %q, want %q", rooted.Field, "baseline.stops[3].id")
	}
	if rooted.Message != err.Message {
		t.Fatalf("message = %q, want %q", rooted.Message, err.Message)
	}
	if err.Field != "route.stops[3].id" {
		t.Fatalf("original field = %q, want it unchanged", err.Field)
	}
}

func TestValidationErrorRootedPrefixesAnUnexpectedField(t *testing.T) {
	err := &ValidationError{Field: "jurisdiction", Message: "must match"}

	if rooted := err.Rooted("proposed"); rooted.Field != "proposed.jurisdiction" {
		t.Fatalf("field = %q, want %q", rooted.Field, "proposed.jurisdiction")
	}
}

func TestValidationErrorRootedKeepsARootOnlyField(t *testing.T) {
	err := &ValidationError{Field: "route", Message: "must be an object"}

	if rooted := err.Rooted("baseline"); rooted.Field != "baseline" {
		t.Fatalf("field = %q, want %q", rooted.Field, "baseline")
	}
}
