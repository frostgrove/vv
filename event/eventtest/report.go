package eventtest

import "strings"

// Three words, never two. A section a store did not claim and a section a store
// could not demonstrate are reported alike, and neither of them is a pass.
//
// The zero value is none of the three, and that is the point of it: a factory
// hook that calls t.Fatal leaves the subtest through runtime.Goexit, so nothing
// inside the section ever returns a verdict and the row the runner initialised
// is the row it keeps. The safe default for a verdict nobody computed is not the
// one word that means the store is correct.
type word uint8

const (
	unreported word = iota
	passed
	notCertified
	failed
)

func (this word) String() string {
	switch this {
	case passed:
		return "passed"
	case notCertified:
		return "not certified"
	case failed:
		return "failed"
	}
	return "not reported"
}

type verdict struct {
	section string
	word    word
	reason  string
}

func (this verdict) line() string {
	line := "eventtest: " + this.section + ": " + this.word.String()
	if this.reason == "" {
		return line
	}
	return line + " — " + this.reason
}

func certified(verdicts []verdict) int {
	count := 0
	for _, given := range verdicts {
		if given.word == passed {
			count++
		}
	}
	return count
}

func (this *probe) verdict() verdict {
	switch {
	case this.broke != "":
		return verdict{section: this.name, word: failed, reason: this.broke}
	case len(this.unmet) > 0:
		return verdict{section: this.name, word: notCertified, reason: strings.Join(this.unmet, "; ")}
	}
	return verdict{section: this.name, word: passed}
}
