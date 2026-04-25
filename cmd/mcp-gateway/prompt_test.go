package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirm_DefaultYesAcceptsEmpty(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got := confirmFromReader(in, &out, "Patch?", true, false)
	if got != true {
		t.Fatalf("want true, got false")
	}
	if !strings.Contains(out.String(), "[Y/n]") {
		t.Fatalf("prompt should show [Y/n], got %q", out.String())
	}
}

func TestConfirm_DefaultNoRejectsEmpty(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got := confirmFromReader(in, &out, "Force?", false, false)
	if got != false {
		t.Fatalf("want false, got true")
	}
	if !strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("prompt should show [y/N], got %q", out.String())
	}
}

func TestConfirm_AcceptsYn(t *testing.T) {
	cases := map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, "YES\n": true,
		"n\n": false, "N\n": false, "no\n": false, "NO\n": false,
	}
	for input, want := range cases {
		in := strings.NewReader(input)
		var out bytes.Buffer
		got := confirmFromReader(in, &out, "?", true, false)
		if got != want {
			t.Errorf("input %q: want %v, got %v", input, want, got)
		}
	}
}

func TestConfirm_AssumeYesShortCircuits(t *testing.T) {
	in := strings.NewReader("garbage that would otherwise fail\n")
	var out bytes.Buffer
	got := confirmFromReader(in, &out, "?", false, true)
	if got != true {
		t.Fatal("assumeYes should always return true")
	}
}
