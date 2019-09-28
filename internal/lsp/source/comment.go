package source

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ulquo = "“"
	urquo = "”"
)

var (
	markdownEscape = regexp.MustCompile(`([\\\x60*{}[\]()#+\-.!_>~|"$%&'\/:;<=?@^])`)

	unicodeQuoteReplacer = strings.NewReplacer("``", ulquo, "''", urquo)
)

// commentEscape escapes comment text for markdown. If nice is set,
// also turn `` into “; and '' into ”;.
func commentEscape(w io.Writer, text string, nice bool) {
	if nice {
		text = convertQuotes(text)
	}
	text = escapeRegex(text)
	w.Write([]byte(text))
}

func convertQuotes(text string) string {
	return unicodeQuoteReplacer.Replace(text)
}

func escapeRegex(text string) string {
	return markdownEscape.ReplaceAllString(text, `\$1`)
}

const (
	// Regexp for Go identifiers
	identRx = `[\pL_][\pL_0-9]*`

	// Regexp for URLs
	// Match parens, and check later for balance - see #5043, #22285
	// Match .,:;?! within path, but not at end - see #18139, #16565
	// This excludes some rare yet valid urls ending in common punctuation
	// in order to allow sentences ending in URLs.

	// protocol (required) e.g. http
	protoPart = `(https?|ftp|file|gopher|mailto|nntp)`
	// host (required) e.g. www.example.com or [::1]:8080
	hostPart = `([a-zA-Z0-9_@\-.\[\]:]+)`
	// path+query+fragment (optional) e.g. /path/index.html?q=foo#bar
	pathPart = `([.,:;?!]*[a-zA-Z0-9$'()*+&#=@~_/\-\[\]%])*`

	urlRx = protoPart + `://` + hostPart + pathPart
)

var (
	matchRx     = regexp.MustCompile(`(` + urlRx + `)|(` + identRx + `)`)
	urlReplacer = strings.NewReplacer(`(`, `\(`, `)`, `\)`)
)

func emphasize(w io.Writer, line string, words map[string]string, nice bool) {
	for {
		m := matchRx.FindStringSubmatchIndex(line)
		if m == nil {
			break
		}
		// m >= 6 (two parenthesized sub-regexps in matchRx, 1st one is urlRx)

		// write text before match
		commentEscape(w, line[0:m[0]], nice)

		// adjust match for URLs
		match := line[m[0]:m[1]]
		if strings.Contains(match, "://") {
			m0, m1 := m[0], m[1]
			for _, s := range []string{"()", "{}", "[]"} {
				open, close := s[:1], s[1:] // E.g., "(" and ")"
				// require opening parentheses before closing parentheses (#22285)
				if i := strings.Index(match, close); i >= 0 && i < strings.Index(match, open) {
					m1 = m0 + i
					match = line[m0:m1]
				}
				// require balanced pairs of parentheses (#5043)
				for i := 0; strings.Count(match, open) != strings.Count(match, close) && i < 10; i++ {
					m1 = strings.LastIndexAny(line[:m1], s)
					match = line[m0:m1]
				}
			}
			if m1 != m[1] {
				// redo matching with shortened line for correct indices
				m = matchRx.FindStringSubmatchIndex(line[:m[0]+len(match)])
			}
		}

		// analyze match
		url := ""
		italics := false
		if words != nil {
			url, italics = words[match]
		}
		if m[2] >= 0 {
			// match against first parenthesized sub-regexp; must be match against urlRx
			if !italics {
				// no alternative URL in words list, use match instead
				url = match
			}
			italics = false // don't italicize URLs
		}

		// write match
		if len(url) > 0 {
			w.Write([]byte(`[`))
		}
		if italics {
			w.Write([]byte(`*`))
		}
		commentEscape(w, match, nice)
		if italics {
			w.Write([]byte(`*`))
		}
		if len(url) > 0 {
			w.Write([]byte(`](`))
			w.Write([]byte(urlReplacer.Replace(url)))
			w.Write([]byte(")"))
		}

		// advance
		line = line[m[1]:]
	}
	commentEscape(w, line, nice)
}

func indentLen(s string) int {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

func isBlank(s string) bool {
	return len(s) == 0 || (len(s) == 1 && s[0] == '\n')
}

func commonPrefix(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return a[0:i]
}

func unindent(block []string) {
	if len(block) == 0 {
		return
	}

	// compute maximum common white prefix
	prefix := block[0][0:indentLen(block[0])]
	for _, line := range block {
		if !isBlank(line) {
			prefix = commonPrefix(prefix, line[0:indentLen(line)])
		}
	}
	n := len(prefix)

	// remove
	for i, line := range block {
		if !isBlank(line) {
			block[i] = line[n:]
		}
	}
}

// heading returns the trimmed line if it passes as a section heading;
// otherwise it returns the empty string.
func heading(line string) string {
	line = strings.TrimSpace(line)
	if len(line) == 0 {
		return ""
	}

	// a heading must start with an uppercase letter
	r, _ := utf8.DecodeRuneInString(line)
	if !unicode.IsLetter(r) || !unicode.IsUpper(r) {
		return ""
	}

	// it must end in a letter or digit:
	r, _ = utf8.DecodeLastRuneInString(line)
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return ""
	}

	// exclude lines with illegal characters. we allow "(),"
	if strings.ContainsAny(line, ";:!?+*/=[]{}_^°&§~%#@<\">\\") {
		return ""
	}

	// allow "'" for possessive "'s" only
	for b := line; ; {
		i := strings.IndexRune(b, '\'')
		if i < 0 {
			break
		}
		if i+1 >= len(b) || b[i+1] != 's' || (i+2 < len(b) && b[i+2] != ' ') {
			return "" // not followed by "s "
		}
		b = b[i+2:]
	}

	// allow "." when followed by non-space
	for b := line; ; {
		i := strings.IndexRune(b, '.')
		if i < 0 {
			break
		}
		if i+1 >= len(b) || b[i+1] == ' ' {
			return "" // not followed by non-space
		}
		b = b[i+1:]
	}

	return line
}

type op int

const (
	opPara op = iota
	opHead
	opPre
)

type block struct {
	op    op
	lines []string
}

var nonAlphaNumRx = regexp.MustCompile(`[^a-zA-Z0-9]`)

func anchorID(line string) string {
	// Add a "hdr-" prefix to avoid conflicting with IDs used for package symbols.
	return "hdr-" + nonAlphaNumRx.ReplaceAllString(line, "_")
}

func blocks(text string) []block {
	var (
		out  []block
		para []string

		lastWasBlank   = false
		lastWasHeading = false
	)

	close := func() {
		if para != nil {
			out = append(out, block{opPara, para})
			para = nil
		}
	}

	lines := strings.SplitAfter(text, "\n")
	unindent(lines)
	for i := 0; i < len(lines); {
		line := lines[i]
		if isBlank(line) {
			// close paragraph
			close()
			i++
			lastWasBlank = true
			continue
		}
		if indentLen(line) > 0 {
			// close paragraph
			close()

			// count indented or blank lines
			j := i + 1
			for j < len(lines) && (isBlank(lines[j]) || indentLen(lines[j]) > 0) {
				j++
			}
			// but not trailing blank lines
			for j > i && isBlank(lines[j-1]) {
				j--
			}
			pre := lines[i:j]
			i = j

			unindent(pre)

			// put those lines in a pre block
			out = append(out, block{opPre, pre})
			lastWasHeading = false
			continue
		}

		if lastWasBlank && !lastWasHeading && i+2 < len(lines) &&
			isBlank(lines[i+1]) && !isBlank(lines[i+2]) && indentLen(lines[i+2]) == 0 {
			// current line is non-blank, surrounded by blank lines
			// and the next non-blank line is not indented: this
			// might be a heading.
			if head := heading(line); head != "" {
				close()
				out = append(out, block{opHead, []string{head}})
				i += 2
				lastWasHeading = true
				continue
			}
		}

		// open paragraph
		lastWasBlank = false
		lastWasHeading = false
		para = append(para, lines[i])
		i++
	}
	close()

	return out
}

// CommentToMarkdown converts comment text to formatted markdown.
// The comment was prepared by DocReader,
// so it is known not to have leading, trailing blank lines
// nor to have trailing spaces at the end of lines.
// The comment markers have already been removed.
//
// Each line is converted into a markdown line and empty lines are just converted to
// newlines. Heading are prefixed with `### ` to make it a markdown heading.
//
// A span of indented lines retains a 4 space prefix block, with the common indent
// prefix removed unless empty, in which case it will be converted to a newline.
//
// URLs in the comment text are converted into links; if the URL also appears
// in the words map, the link is taken from the map (if the corresponding map
// value is the empty string, the URL is not converted into a link).
//
// Go identifiers that appear in the words map are italicized; if the corresponding
// map value is not the empty string, it is considered a URL and the word is converted
// into a link.
func CommentToMarkdown(w io.Writer, text string, words map[string]string) {
	isFirstLine := true
	for _, b := range blocks(text) {
		switch b.op {
		case opPara:
			if !isFirstLine {
				w.Write([]byte("\n"))
			}

			for _, line := range b.lines {
				emphasize(w, line, words, true)
			}
		case opHead:
			if !isFirstLine {
				w.Write([]byte("\n"))
			}
			w.Write([]byte("\n"))

			for _, line := range b.lines {
				w.Write([]byte("### "))
				commentEscape(w, line, true)
				w.Write([]byte("\n"))
			}
		case opPre:
			if !isFirstLine {
				w.Write([]byte("\n"))
			}
			w.Write([]byte("\n"))

			for _, line := range b.lines {
				if isBlank(line) {
					w.Write([]byte("\n"))
				} else {
					w.Write([]byte("    "))
					w.Write([]byte(line))
					w.Write([]byte("\n"))
				}
			}
		}
		isFirstLine = false
	}

}

// ToMarkdownString is similair to ToMarkdown except that it returns a string
func ToMarkdownString(text string, words map[string]string) string {
	buf := &bytes.Buffer{}
	CommentToMarkdown(buf, text, words)
	return buf.String()
}
