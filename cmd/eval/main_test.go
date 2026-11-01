package main

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMainMissingBaseline(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--ci", "-baseline", filepath.Join(t.TempDir(), "nope.json")}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "--update-baseline") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}

func TestMainCIForcesOneSeed(t *testing.T) {
	if got := seedsFor(42, 3, true); !slices.Equal(got, []int64{42}) {
		t.Fatalf("ci: %v", got)
	}
	if got := seedsFor(42, 3, false); !slices.Equal(got, []int64{42, 43, 44}) {
		t.Fatalf("plain: %v", got)
	}
	if got := seedsFor(7, 0, false); !slices.Equal(got, []int64{7}) {
		t.Fatalf("zero seeds: %v", got)
	}
}

func TestMainRerankFlagInvalid(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-rerank", "bogus"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "bogus") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}

func TestMainCIRejectsScale(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--ci", "-scale", "2", "-baseline", filepath.Join(t.TempDir(), "x")}, &out, &errb); code != 2 {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}
