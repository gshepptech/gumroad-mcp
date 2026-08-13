package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// zeroDecimalCurrencies have no minor unit, so their amounts are already whole.
var zeroDecimalCurrencies = map[string]bool{
	"jpy": true, "krw": true, "vnd": true, "clp": true, "isk": true,
	"bif": true, "djf": true, "gnf": true, "kmf": true, "mga": true,
	"pyg": true, "rwf": true, "ugx": true, "vuv": true,
	"xaf": true, "xof": true, "xpf": true,
}

var currencySymbols = map[string]string{
	"usd": "$", "eur": "€", "gbp": "£", "jpy": "¥",
	"aud": "A$", "cad": "C$", "nzd": "NZ$", "inr": "₹",
}

// money renders an amount held in a currency's smallest unit.
func money(minorUnits int, currency string) string {
	c := strings.ToLower(currency)
	if c == "" {
		c = "usd"
	}
	if zeroDecimalCurrencies[c] {
		return fmt.Sprintf("%d %s", minorUnits, strings.ToUpper(c))
	}
	sign := ""
	if minorUnits < 0 {
		sign, minorUnits = "-", -minorUnits
	}
	amount := fmt.Sprintf("%s.%02d", group(minorUnits/100), minorUnits%100)
	if sym := currencySymbols[c]; sym != "" {
		return sign + sym + amount
	}
	return sign + amount + " " + strings.ToUpper(c)
}

// group inserts thousands separators, so a year of revenue reads as
// $47,382.00 rather than $47382.00.
func group(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// trunc shortens a string to fit a column, accounting for runes not bytes.
func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if n <= 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// atoiSafe reads the leading digits of a string, returning 0 when there are none.
func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func countOrDash(n *int) string {
	if n == nil {
		return "–"
	}
	return strconv.Itoa(*n)
}

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

var tagRE = regexp.MustCompile(`<[^>]*>`)

// stripTags renders an HTML email body as rough plain text for the detail pane.
func stripTags(html string) string {
	s := strings.NewReplacer(
		"<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"</p>", "\n\n", "</div>", "\n", "</li>", "\n",
		"<li>", "  • ",
	).Replace(html)
	s = tagRE.ReplaceAllString(s, "")
	s = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
	).Replace(s)
	// Collapse the blank-line runs the tag stripping leaves behind.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
