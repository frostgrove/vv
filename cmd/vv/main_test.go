package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestCacheGenerateSubcommandHasDedicatedFlags(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"generate", "cache", "-help"}, &output, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("cache help error = %v", err)
	}
	help := output.String()
	if !strings.Contains(help, "-manifest") || !strings.Contains(help, "-check") || strings.Contains(help, "-adapter") {
		t.Fatalf("cache help dispatched to the wrong command:\n%s", help)
	}
}

func TestJobsGenerateSubcommandDoesNotExist(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"generate", "jobs"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("jobs generator error = %v", err)
	}
}

func TestLegacyGenerateKeepsModelFlags(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"generate", "-help"}, &output, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("model help error = %v", err)
	}
	help := output.String()
	if !strings.Contains(help, "-recursive") || !strings.Contains(help, "-adapter") || strings.Contains(help, "-manifest") {
		t.Fatalf("legacy generate flags changed:\n%s", help)
	}
}
