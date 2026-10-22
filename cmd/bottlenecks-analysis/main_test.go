package main

import (
	"testing"
	"time"
)

func TestAnalysisBudget(t *testing.T) {
	if got := analysisBudget(true); got != 15*time.Minute {
		t.Fatalf("global budget: %v", got)
	}
	if got := analysisBudget(false); got != 5*time.Minute {
		t.Fatalf("single-target budget: %v", got)
	}
}
