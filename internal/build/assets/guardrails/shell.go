package main

import (
	"errors"
	"regexp"
	"strings"
)

// shortcut: a small POSIX-ish word splitter, not a shell parser. It knows
// quotes, escapes, $(…)/`…` spans and the list operators, which is what the
// checks read; here-docs, functions and control flow fall through as plain
// words. Upgrade to mvdan.cc/sh if a check ever needs real syntax.

type quoting int

const (
	unquoted quoting = iota
	doubleQuoted
	singleQuoted
)

type segment struct {
	text  string
	quote quoting
}

// word is one shell word, kept as its quoted segments so expansion can skip
// what single quotes protect.
type word []segment

var errUnterminated = errors.New("unterminated quote or substitution")

// tokenize splits a command line into simple commands at ; & | && || ( ) and
// newlines.
func tokenize(line string) ([][]word, error) {
	var (
		commands [][]word
		current  []word
		w        word
		buf      strings.Builder
		quote    = unquoted
		inWord   bool
	)
	flushSeg := func() {
		if buf.Len() > 0 || quote != unquoted {
			w = append(w, segment{buf.String(), quote})
		}
		buf.Reset()
	}
	endWord := func() {
		flushSeg()
		if inWord {
			current = append(current, w)
		}
		w, inWord = nil, false
	}
	endCommand := func() {
		endWord()
		if len(current) > 0 {
			commands = append(commands, current)
		}
		current = nil
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote == singleQuoted:
			if c == '\'' {
				flushSeg()
				quote = unquoted
			} else {
				buf.WriteByte(c)
			}
		case c == '\\' && i+1 < len(line):
			i++
			buf.WriteByte(line[i])
			inWord = true
		case quote == doubleQuoted && c == '"':
			flushSeg()
			quote = unquoted
		case c == '$' && i+1 < len(line) && line[i+1] == '(' || c == '`':
			end := substitutionEnd(line, i)
			if end < 0 {
				return nil, errUnterminated
			}
			buf.WriteString(line[i : end+1])
			i, inWord = end, true
		case quote == doubleQuoted:
			buf.WriteByte(c)
		case c == '\'' || c == '"':
			flushSeg()
			quote = singleQuoted
			if c == '"' {
				quote = doubleQuoted
			}
			inWord = true
		case c == '#' && !inWord:
			for i+1 < len(line) && line[i+1] != '\n' {
				i++
			}
		case c == ' ' || c == '\t':
			endWord()
		case strings.IndexByte(";&|()\n", c) >= 0:
			endCommand()
		default:
			buf.WriteByte(c)
			inWord = true
		}
	}
	if quote != unquoted {
		return nil, errUnterminated
	}
	endCommand()
	return commands, nil
}

// substitutionEnd returns the index closing the $(…) or `…` opened at i, or -1.
func substitutionEnd(line string, i int) int {
	if line[i] == '`' {
		if j := strings.IndexByte(line[i+1:], '`'); j >= 0 {
			return i + 1 + j
		}
		return -1
	}
	depth := 0
	for j := i + 1; j < len(line); j++ {
		switch line[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

var variable = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?|\$\(pwd\)|` + "`pwd`")

// expand applies the expansions the checks need: a leading unquoted ~, $VAR,
// ${VAR} and the working directory's $(pwd) / `pwd`. Unknown names stay as
// written.
func (w word) expand(vars map[string]string) string {
	var b strings.Builder
	for k, s := range w {
		text := s.text
		if s.quote == singleQuoted {
			b.WriteString(text)
			continue
		}
		if k == 0 && s.quote == unquoted && (text == "~" || strings.HasPrefix(text, "~/")) {
			text = vars["HOME"] + text[1:]
		}
		b.WriteString(variable.ReplaceAllStringFunc(text, func(m string) string {
			if m == "$(pwd)" || m == "`pwd`" {
				return vars["PWD"]
			}
			name := strings.Trim(m, "${}")
			if v, ok := vars[name]; ok {
				return v
			}
			return m
		}))
	}
	return b.String()
}

// assignment reports whether the word is a NAME=value assignment.
func (w word) assignment() bool {
	return len(w) > 0 && w[0].quote == unquoted && assignmentPrefix.MatchString(w[0].text)
}

var assignmentPrefix = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
