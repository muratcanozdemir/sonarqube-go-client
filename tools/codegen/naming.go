package main

import (
	"regexp"
	"strings"
	"unicode"
)

var initialisms = map[string]string{
	"id": "ID", "ids": "IDs", "url": "URL", "urls": "URLs", "uuid": "UUID", "uuids": "UUIDs",
	"api": "API", "json": "JSON", "http": "HTTP", "html": "HTML", "sql": "SQL", "xml": "XML",
	"csv": "CSV", "ui": "UI", "ip": "IP", "ssl": "SSL", "uri": "URI", "pat": "PAT", "sbom": "SBOM",
	"jwt": "JWT", "scm": "SCM", "llm": "LLM", "ai": "AI", "sso": "SSO", "saml": "SAML", "dop": "DOP",
	"jre": "JRE", "jres": "JREs", "cwe": "CWE", "cve": "CVE", "sans": "SANS", "cli": "CLI", "clis": "CLIs",
}

var wordSplit = regexp.MustCompile(`[^A-Za-z0-9]+`)

// splitWords splits identifiers in camelCase, snake_case, kebab-case or
// dotted form into lower-case words.
func splitWords(s string) []string {
	var words []string
	for _, part := range wordSplit.Split(s, -1) {
		if part == "" {
			continue
		}
		rs := []rune(part)
		start := 0
		for i := 1; i < len(rs); i++ {
			prev, cur := rs[i-1], rs[i]
			next := rune(0)
			if i+1 < len(rs) {
				next = rs[i+1]
			}
			boundary := (unicode.IsLower(prev) && unicode.IsUpper(cur)) ||
				(unicode.IsUpper(prev) && unicode.IsUpper(cur) && next != 0 && unicode.IsLower(next))
			if boundary {
				words = append(words, strings.ToLower(string(rs[start:i])))
				start = i
			}
		}
		words = append(words, strings.ToLower(string(rs[start:])))
	}
	return words
}

// goName converts an API identifier into an exported Go identifier.
func goName(s string) string {
	var b strings.Builder
	for _, w := range splitWords(s) {
		if v, ok := initialisms[w]; ok {
			b.WriteString(v)
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	out := b.String()
	if out == "" {
		return "X"
	}
	if unicode.IsDigit(rune(out[0])) {
		out = "X" + out
	}
	return out
}

// uniqueNamer hands out unique identifiers within one scope.
type uniqueNamer map[string]bool

func (u uniqueNamer) name(n string) string {
	if !u[n] {
		u[n] = true
		return n
	}
	for i := 2; ; i++ {
		c := n + itoa(i)
		if !u[c] {
			u[c] = true
			return c
		}
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}

var tagRE = regexp.MustCompile(`<[^>]*>`)
var spaceRE = regexp.MustCompile(`\s+`)

// cleanDoc strips HTML and collapses whitespace for use in Go comments.
func cleanDoc(s string) string {
	s = strings.NewReplacer("<br/>", " ", "<br>", " ", "<li>", " - ", "&lt;", "<", "&gt;", ">", "&amp;", "&", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ").Replace(s)
	s = tagRE.ReplaceAllString(s, " ")
	s = spaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// wrapComment renders text as a Go comment block with the given indent.
func wrapComment(text, indent string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, para := range strings.Split(text, "\n") {
		line := indent + "//"
		words := strings.Fields(para)
		if len(words) == 0 {
			b.WriteString(indent + "//\n")
			continue
		}
		for _, w := range words {
			if len(line)+1+len(w) > 100 && line != indent+"//" {
				b.WriteString(line + "\n")
				line = indent + "//"
			}
			line += " " + w
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
