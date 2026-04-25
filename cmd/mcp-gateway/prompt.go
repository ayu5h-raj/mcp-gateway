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
func confirm(prompt string, defaultYes, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return defaultYes
	}
	return confirmFromReader(os.Stdin, os.Stdout, prompt, defaultYes, false)
}

// confirmFromReader is the testable core: reads one line from in, writes
// the prompt to out, returns the decision. Empty input picks defaultYes.
// Anything starting with y/Y is yes; n/N is no; other input re-prompts up
// to 3 times then falls back to defaultYes.
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
		fmt.Fprintf(out, "%s %s ", prompt, suffix)
		line, err := br.ReadString('\n')
		if err != nil && line == "" {
			return defaultYes
		}
		line = strings.TrimSpace(strings.ToLower(line))
		switch {
		case line == "":
			return defaultYes
		case line == "y" || line == "yes":
			return true
		case line == "n" || line == "no":
			return false
		default:
			fmt.Fprintln(out, "please answer y or n")
		}
	}
	return defaultYes
}
