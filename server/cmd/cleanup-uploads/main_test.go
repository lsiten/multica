package main

import (
	"context"
	"io"
	"testing"
)

func TestRunRejectsUnsafeOptionsBeforeOpeningDatabase(t *testing.T) {
	for _, args := range [][]string{
		{"--retention", "0"}, {"--retention", "23h"}, {"--limit", "0"},
		{"--limit", "1001"}, {"--workspace", "invalid"}, {"unexpected"},
	} {
		t.Run(args[0], func(t *testing.T) {
			// Given
			t.Setenv("DATABASE_URL", "")

			// When
			err := run(context.Background(), args, io.Discard)

			// Then
			if err == nil {
				t.Fatalf("accepted unsafe options: %v", args)
			}
		})
	}
}
