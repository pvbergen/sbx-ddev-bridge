// Package sqlread tells whether a MySQL/MariaDB query text only reads. It is
// a prompt filter for agents, not a security boundary: functions with side
// effects inside a SELECT (SLEEP, GET_LOCK, stored functions) pass.
package sqlread

import (
	"fmt"
	"strings"
)

// readStarts are the statements that only read.
var readStarts = map[string]bool{"SELECT": true, "SHOW": true, "DESCRIBE": true, "DESC": true, "EXPLAIN": true}

// Check returns "" if every statement in sql only reads, otherwise the reason
// it does not count as a read. Statements are split on `;` outside quotes and
// comments, so several reads in one text are fine.
func Check(sql string) string {
	stmts, err := split(sql)
	if err != "" {
		return err
	}
	if len(stmts) == 0 {
		return "no SQL statement found"
	}
	for _, words := range stmts {
		if !readStarts[words[0]] {
			return fmt.Sprintf("%s is not a read (only SELECT, SHOW, DESCRIBE and EXPLAIN are)", words[0])
		}
		for i, w := range words {
			next := ""
			if i+1 < len(words) {
				next = words[i+1]
			}
			switch {
			case w == "INTO":
				return "SELECT … INTO writes a file or variable"
			case w == "FOR" && (next == "UPDATE" || next == "SHARE"), w == "LOCK":
				return "the query locks rows"
			case w == "ANALYZE":
				return "EXPLAIN ANALYZE runs the statement"
			}
		}
	}
	return ""
}

// split returns the upper-cased words of each non-empty statement, or a
// reason why the text can't be judged. A statement must start with a word,
// optionally after opening parentheses.
func split(s string) ([][]string, string) {
	const notKeyword = "a statement does not start with a keyword"
	var stmts [][]string
	var cur []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			cur = append(cur, strings.ToUpper(word.String()))
			word.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			flush()
			if len(cur) == 0 {
				return nil, notKeyword
			}
			if i = closeQuote(s, i); i < 0 {
				return nil, "unterminated quote"
			}
		case c == '#' || c == '-' && strings.HasPrefix(s[i:], "--") && (i+2 == len(s) || isSpace(s[i+2])):
			flush()
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && strings.HasPrefix(s[i:], "/*"):
			flush()
			if strings.HasPrefix(s[i:], "/*!") || strings.HasPrefix(s[i:], "/*M!") {
				return nil, "executable comments (/*! … */) run their content"
			}
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, "unterminated comment"
			}
			i += 2 + end + 1
		case c == '\\':
			return nil, "client commands (\\…) are not plain SQL"
		case c == ';':
			flush()
			if len(cur) > 0 {
				stmts = append(stmts, cur)
			}
			cur = nil
		case isWord(c):
			word.WriteByte(c)
		default:
			flush()
			if len(cur) == 0 && c != '(' && !isSpace(c) {
				return nil, notKeyword
			}
		}
	}
	flush()
	if len(cur) > 0 {
		stmts = append(stmts, cur)
	}
	return stmts, ""
}

// closeQuote returns the index of the quote closing the one at i, or -1.
// Backslash escapes apply inside '…' and "…"; a doubled quote is read as two
// adjacent quoted parts, which ends up the same.
func closeQuote(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] == '\\' && q != '`':
			j++
		case s[j] == q:
			return j
		}
	}
	return -1
}

func isWord(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
