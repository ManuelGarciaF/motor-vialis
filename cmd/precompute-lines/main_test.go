package main

import (
	"context"
	"io"
	"testing"
)

func TestRunRejectsInvalidFlagsBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-jurisdiction=caba"},
		{"-jurisdiction=guess", "-model-version=test"},
		{"-jurisdiction=caba", "-model-version=test", "-timeout=0s"},
		{"-jurisdiction=caba", "-model-version=test", "-limit=-1"},
		{"-jurisdiction=caba", "-model-version=test", "unexpected"},
	} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
