package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestInventoryCLIRejectsAmbiguousOrUnboundedOptions(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-backend", "unknown", "-output", "out"}, {"-backend", "local_file"},
		{"-backend", "local_file", "-output", "out", "-max-entries", "0"},
		{"-backend", "local_file", "-output", "out", "-timeout", "31m"},
		{"-backend", "local_file", "-output", "out", "-timeout", "0s"},
		{"-backend", "local_file", "-output", "out", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
			t.Fatal(args, code, stdout.String(), stderr.String())
		}
	}
}

func TestInventoryCLIDoesNotPrintConfigurationSecrets(t *testing.T) {
	t.Setenv("MEDIA_STORAGE_ADAPTER", "secret-invalid-adapter-marker")
	t.Setenv("DATABASE_URL", "postgres://private-secret@example.invalid/private")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-backend", "local_file", "-output", "unused"}, &stdout, &stderr); code != 1 {
		t.Fatal(code)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "secret") || !strings.Contains(stderr.String(), "configuration") {
		t.Fatal(stdout.String(), stderr.String())
	}
}
