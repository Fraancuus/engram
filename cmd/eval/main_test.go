package main

import (
	"bytes"
	"errors"
	"os"
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
	for _, args := range [][]string{{"--ci", "-scale", "2"}, {"--ci", "-days", "30"}, {"--update-baseline", "-scale", "2"}} {
		out.Reset()
		errb.Reset()
		if code := run(append(args, "-baseline", filepath.Join(t.TempDir(), "x")), &out, &errb); code != 1 || out.Len() != 0 {
			t.Fatalf("%v: code %d stdout %q stderr %q", args, code, out.String(), errb.String())
		}
	}
}

// Not parallel: it swaps the package-level checkArch.
func TestMainUpdateRefusesNonAmd64(t *testing.T) {
	old := checkArch
	checkArch = func() error { return errors.New("baseline on arm64: unsupported") }
	t.Cleanup(func() { checkArch = old })
	path := filepath.Join(t.TempDir(), "b.json")
	jsonPath := filepath.Join(t.TempDir(), "r.json")
	var out, errb bytes.Buffer
	code := run([]string{"--update-baseline", "-baseline", path, "-json", jsonPath}, &out, &errb)
	if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), "arm64") {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
	}
	for _, f := range []string{path, jsonPath} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s written", f)
		}
	}
}

func TestMainBadBaselineNoUpdateHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"--ci", "-baseline", path}, &out, &errb); code != 1 || strings.Contains(errb.String(), "--update-baseline") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
}

func TestRunRejectsBadLambda(t *testing.T) {
	for _, l := range []string{"-0.1", "NaN", "Inf"} {
		var out, errb bytes.Buffer
		if code := run([]string{"-lambda", l}, &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "lambda") {
			t.Errorf("%s: code %d stdout %q stderr %q", l, code, out.String(), errb.String())
		}
	}
}

func TestRunRejectsUnknownMix(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-mix", "weekend"}, &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "weekend") {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
	}
}

func TestUpdateBaselineRefusesStress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.json")
	var out, errb bytes.Buffer
	code := run([]string{"--update-baseline", "-mix", "stress", "-baseline", path}, &out, &errb)
	if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), "agent") {
		t.Fatalf("code %d stdout %q stderr %q", code, out.String(), errb.String())
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("baseline written")
	}
}
