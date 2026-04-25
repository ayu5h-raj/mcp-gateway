package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// confirm prints prompt + a [Y/n] (defaultYes) or [y/N] suffix and reads
// a yes/no answer from stdin. If assumeYes is true, returns true without
// reading. If stdin is not a TTY and assumeYes is false, returns the
// default — never blocks on a piped command line.
//
// Thin TTY-check wrapper around confirmFromReader; all logic lives there.
func confirm(prompt string, defaultYes, assumeYes bool) bool {
	if !assumeYes && !term.IsTerminal(int(os.Stdin.Fd())) {
		return defaultYes
	}
	return confirmFromReader(os.Stdin, os.Stdout, prompt, defaultYes, assumeYes)
}

// confirmFromReader is the testable core: reads one line from in, writes
// the prompt to out, returns the decision. assumeYes short-circuits to
// true. Empty input picks defaultYes. Exactly y/yes (case-insensitive) is
// yes; exactly n/no is no; other input re-prompts up to 3 times then
// falls back to defaultYes.
func confirmFromReader(in io.Reader, out io.Writer, prompt string, defaultYes, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	br := bufio.NewReader(in)
	for attempt := 0; attempt < 3; attempt++ {
		_, _ = fmt.Fprintf(out, "%s %s ", prompt, suffix)
		line, err := br.ReadString('\n')
		if err != nil && line == "" {
			return defaultYes
		}
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "":
			return defaultYes
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			_, _ = fmt.Fprintln(out, "please answer y or n")
		}
	}
	return defaultYes
}
