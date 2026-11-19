package main

import (
	"errors"
	"strings"
)

// lexShell splits a Bash command line into simple commands. It is a
// conservative reading for the PreToolUse hook, not a full shell parser:
// operators (; & | && || newline parentheses) end a command, redirection
// targets are dropped, and commands inside $(…), `…` and <(…) are returned as
// commands of their own. Words with $-expansions or unquoted globs are marked
// dynamic.
func lexShell(s string) ([][]shWord, error) {
	l := lexer{s: s}
	if err := l.run(false); err != nil {
		return nil, err
	}
	return l.cmds, nil
}

type lexer struct {
	s    string
	i    int
	cmds [][]shWord
}

// run lexes until the end of input or, when nested, the ) that closes a $( or
// <(; it leaves l.i after that ).
func (l *lexer) run(nested bool) error {
	var cur []shWord
	var word strings.Builder
	inWord, dynamic, redirect := false, false, false
	depth := 0
	endWord := func() {
		if inWord {
			if !redirect {
				cur = append(cur, shWord{text: word.String(), dynamic: dynamic})
			}
			redirect = false
		}
		word.Reset()
		inWord, dynamic = false, false
	}
	endCmd := func() {
		endWord()
		if len(cur) > 0 {
			l.cmds = append(l.cmds, cur)
		}
		cur = nil
	}
	for l.i < len(l.s) {
		c := l.s[l.i]
		switch {
		case c == ' ' || c == '\t':
			endWord()
			l.i++
		case c == '\\':
			// a backslash-newline joins lines and is not part of a word
			if l.i+1 < len(l.s) && l.s[l.i+1] != '\n' {
				word.WriteByte(l.s[l.i+1])
				inWord = true
			}
			l.i += 2
		case c == '\'':
			end := strings.IndexByte(l.s[l.i+1:], '\'')
			if end < 0 {
				return errors.New("unterminated quote")
			}
			word.WriteString(l.s[l.i+1 : l.i+1+end])
			inWord = true
			l.i += end + 2
		case c == '"':
			inWord = true
			l.i++
			if err := l.double(&word, &dynamic); err != nil {
				return err
			}
		case c == '$' || c == '`':
			inWord = true
			if err := l.expansion(&word, &dynamic); err != nil {
				return err
			}
		case c == '#' && !inWord:
			for l.i < len(l.s) && l.s[l.i] != '\n' {
				l.i++
			}
		case c == '<' || c == '>':
			if l.i+1 < len(l.s) && l.s[l.i+1] == '(' {
				endWord()
				l.i += 2
				if err := l.run(true); err != nil {
					return err
				}
				continue
			}
			// a number right before is the file descriptor, not an argument
			if inWord && !dynamic && isDigits(word.String()) {
				word.Reset()
				inWord = false
			}
			endWord()
			for l.i < len(l.s) && strings.IndexByte("<>&|", l.s[l.i]) >= 0 {
				l.i++
			}
			redirect = true
		case c == '(':
			endCmd()
			depth++
			l.i++
		case c == ')':
			endCmd()
			l.i++
			if depth == 0 && nested {
				return nil
			}
			depth--
		case c == ';' || c == '&' || c == '|' || c == '\n':
			endCmd()
			l.i++
		default:
			if c == '*' || c == '?' || c == '[' {
				dynamic = true
			}
			word.WriteByte(c)
			inWord = true
			l.i++
		}
	}
	if nested {
		return errors.New("unterminated $(")
	}
	endCmd()
	return nil
}

// double reads a "…" string; l.i is just after the opening quote.
func (l *lexer) double(word *strings.Builder, dynamic *bool) error {
	for l.i < len(l.s) {
		c := l.s[l.i]
		switch {
		case c == '"':
			l.i++
			return nil
		case c == '\\' && l.i+1 < len(l.s) && strings.IndexByte("\"\\$`\n", l.s[l.i+1]) >= 0:
			if l.s[l.i+1] != '\n' {
				word.WriteByte(l.s[l.i+1])
			}
			l.i += 2
		case c == '$' || c == '`':
			if err := l.expansion(word, dynamic); err != nil {
				return err
			}
		default:
			word.WriteByte(c)
			l.i++
		}
	}
	return errors.New("unterminated quote")
}

// expansion reads $…, $(…), ${…}, $'…' or `…` at l.i. The word becomes
// dynamic; commands inside $(…) and `…` are lexed as commands of their own.
func (l *lexer) expansion(word *strings.Builder, dynamic *bool) error {
	rest := l.s[l.i:]
	switch {
	case strings.HasPrefix(rest, "$(("):
		end := strings.Index(rest, "))")
		if end < 0 {
			return errors.New("unterminated $((")
		}
		l.i += end + 2
	case strings.HasPrefix(rest, "$("):
		l.i += 2
		if err := l.run(true); err != nil {
			return err
		}
	case rest[0] == '`':
		end := strings.IndexByte(rest[1:], '`')
		if end < 0 {
			return errors.New("unterminated `")
		}
		inner, err := lexShell(rest[1 : 1+end])
		if err != nil {
			return err
		}
		l.cmds = append(l.cmds, inner...)
		l.i += end + 2
	case strings.HasPrefix(rest, "${"):
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return errors.New("unterminated ${")
		}
		l.i += end + 1
	case strings.HasPrefix(rest, "$'"):
		end := strings.IndexByte(rest[2:], '\'')
		if end < 0 {
			return errors.New("unterminated $'")
		}
		l.i += end + 3
	case len(rest) > 1 && (isNameChar(rest[1]) || strings.IndexByte("@*#?$!-", rest[1]) >= 0):
		j := 2
		if isNameChar(rest[1]) {
			for j < len(rest) && isNameChar(rest[j]) {
				j++
			}
		}
		l.i += j
	default: // a lone $
		word.WriteByte('$')
		l.i++
		return nil
	}
	*dynamic = true
	return nil
}

func isNameChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
