package main

import (
	"errors"
	"regexp"
	"strings"
)

// shortcut: a small POSIX-ish word splitter, not a shell parser. It knows
// quotes, escapes and line continuations, $(…)/`…` spans (whose contents are
// checked as commands of their own), here-document bodies (dropped as text)
// and the list operators; the checker strips control-flow keywords. Functions,
// case patterns and arithmetic fall through as plain words. Upgrade to
// mvdan.cc/sh if a check ever needs real syntax.

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

// tokenizer holds the state of one tokenize pass.
type tokenizer struct {
	line     string
	commands [][]word
	subs     []string // inner text of every $(…) and `…`
	current  []word
	w        word
	buf      strings.Builder
	quote    quoting
	inWord   bool
}

// tokenize splits a command line into simple commands at ; & | && || ( ) and
// newlines, and returns the inner text of its command substitutions.
func tokenize(line string) ([][]word, []string, error) {
	line = stripHeredocBodies(line)
	t := &tokenizer{line: line}
	for i := 0; i < len(line); i++ {
		last, err := t.step(i)
		if err != nil {
			return nil, nil, err
		}
		i = last
	}
	if t.quote != unquoted {
		return nil, nil, errUnterminated
	}
	t.endCommand()
	return t.commands, t.subs, nil
}

var heredocStart = regexp.MustCompile(`<<(-?)[ \t]*(['"]?)([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// stripHeredocBodies drops every here-document body, from the line after its
// << operator through its delimiter line: the body is text the command reads,
// not commands, and may hold anything — unbalanced parentheses included.
func stripHeredocBodies(line string) string {
	from := 0
	for {
		loc := heredocStart.FindStringSubmatchIndex(line[from:])
		if loc == nil {
			return line
		}
		end := from + loc[1]
		if start := from + loc[0]; start > 0 && line[start-1] == '<' {
			from = end // <<<word is a here-string: no body
			continue
		}
		nl := strings.IndexByte(line[end:], '\n')
		if nl < 0 {
			return line
		}
		bodyStart := end + nl + 1
		word := line[from+loc[6] : from+loc[7]]
		stripTabs := loc[3] > loc[2]
		bodyEnd := heredocEnd(line, bodyStart, word, stripTabs)
		line = line[:bodyStart] + line[bodyEnd:]
		from = bodyStart
	}
}

// heredocEnd returns the index just past the delimiter line that closes a
// here-document body starting at start, or len(line) when none does.
func heredocEnd(line string, start int, word string, stripTabs bool) int {
	for k := start; k < len(line); {
		nl := strings.IndexByte(line[k:], '\n')
		next := len(line)
		if nl >= 0 {
			next = k + nl + 1
		}
		text := strings.TrimSuffix(line[k:next], "\n")
		if stripTabs {
			text = strings.TrimLeft(text, "\t")
		}
		if text == word {
			return next
		}
		k = next
	}
	return len(line)
}

// step consumes the character at i, and whatever belongs with it, and
// returns the index of the last character it consumed.
func (t *tokenizer) step(i int) (int, error) {
	switch t.quote {
	case singleQuoted:
		t.inSingle(t.line[i])
		return i, nil
	case doubleQuoted:
		return t.inDouble(i)
	default:
		return t.inUnquoted(i)
	}
}

func (t *tokenizer) inSingle(c byte) {
	if c == '\'' {
		t.flushSeg()
		t.quote = unquoted
		return
	}
	t.buf.WriteByte(c)
}

func (t *tokenizer) inDouble(i int) (int, error) {
	c := t.line[i]
	switch {
	case c == '\\' && i+1 < len(t.line):
		return t.escape(i), nil
	case c == '"':
		t.flushSeg()
		t.quote = unquoted
	case t.substitutionStart(i):
		return t.substitution(i)
	default:
		t.buf.WriteByte(c)
	}
	return i, nil
}

func (t *tokenizer) inUnquoted(i int) (int, error) {
	c := t.line[i]
	switch {
	case c == '\\' && i+1 < len(t.line):
		return t.escape(i), nil
	case t.substitutionStart(i):
		return t.substitution(i)
	case c == '\'' || c == '"':
		t.flushSeg()
		t.quote = singleQuoted
		if c == '"' {
			t.quote = doubleQuoted
		}
		t.inWord = true
	case c == '#' && !t.inWord:
		return t.commentEnd(i), nil
	case c == ' ' || c == '\t':
		t.endWord()
	case c == '&' && t.redirectionAmpersand(i):
		t.buf.WriteByte(c)
		t.inWord = true
	case strings.IndexByte(";&|()\n", c) >= 0:
		t.endCommand()
	default:
		t.buf.WriteByte(c)
		t.inWord = true
	}
	return i, nil
}

// redirectionAmpersand reports whether the & at i belongs to a redirection
// (2>&1, >&2, &>file) rather than ending a command.
func (t *tokenizer) redirectionAmpersand(i int) bool {
	prev := t.buf.String()
	return strings.HasSuffix(prev, ">") || strings.HasSuffix(prev, "<") || i+1 < len(t.line) && t.line[i+1] == '>'
}

// escape keeps the character after a backslash literal: its own segment,
// marked like single-quoted text so expansion leaves it alone. A backslash
// before a newline is a line continuation: both go.
func (t *tokenizer) escape(i int) int {
	if t.line[i+1] == '\n' {
		return i + 1
	}
	quote := t.quote
	t.flushSeg()
	t.w = append(t.w, segment{t.line[i+1 : i+2], singleQuoted})
	t.quote = quote
	t.inWord = true
	return i + 1
}

func (t *tokenizer) substitutionStart(i int) bool {
	c := t.line[i]
	return c == '`' || c == '$' && i+1 < len(t.line) && t.line[i+1] == '('
}

// substitution keeps a $(…) or `…` span verbatim in the current word.
func (t *tokenizer) substitution(i int) (int, error) {
	end := substitutionEnd(t.line, i)
	if end < 0 {
		return 0, errUnterminated
	}
	t.buf.WriteString(t.line[i : end+1])
	t.inWord = true
	inner := t.line[i+1 : end] // `…`
	if t.line[i] == '$' {
		inner = t.line[i+2 : end] // $(…)
	}
	t.subs = append(t.subs, inner)
	return end, nil
}

// commentEnd skips a comment up to, not including, the newline that ends it.
func (t *tokenizer) commentEnd(i int) int {
	for i+1 < len(t.line) && t.line[i+1] != '\n' {
		i++
	}
	return i
}

func (t *tokenizer) flushSeg() {
	if t.buf.Len() > 0 || t.quote != unquoted {
		t.w = append(t.w, segment{t.buf.String(), t.quote})
	}
	t.buf.Reset()
}

func (t *tokenizer) endWord() {
	t.flushSeg()
	if t.inWord {
		t.current = append(t.current, t.w)
	}
	t.w, t.inWord = nil, false
}

func (t *tokenizer) endCommand() {
	t.endWord()
	if len(t.current) > 0 {
		t.commands = append(t.commands, t.current)
	}
	t.current = nil
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
		case '\\':
			j++
		case '\'', '"':
			// A quoted ( or ) does not count.
			if j = quotedEnd(line, j); j < 0 {
				return -1
			}
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return j
			}
		}
	}
	return -1
}

// quotedEnd returns the index of the quote closing the one at i, or -1.
func quotedEnd(line string, i int) int {
	for j := i + 1; j < len(line); j++ {
		switch {
		case line[j] == line[i]:
			return j
		case line[j] == '\\' && line[i] == '"':
			j++
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

// unquotedStart reports whether the word begins outside quotes, as a shell
// operator such as a redirection must.
func (w word) unquotedStart() bool {
	return len(w) > 0 && w[0].quote == unquoted
}

// assignment reports whether the word is a NAME=value assignment.
func (w word) assignment() bool {
	return len(w) > 0 && w[0].quote == unquoted && assignmentPrefix.MatchString(w[0].text)
}

var assignmentPrefix = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
