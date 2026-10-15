// Package redact masks personal data in conversation text (best effort).
package redact

import (
	"html"
	"regexp"
	"strings"
)

// Options controls optional masking.
type Options struct {
	// MaskOrders also masks order numbers in the F + 9 digits format.
	MaskOrders bool
}

const pre = `(^|[^\w.,+/-]|[A-Za-z]\.)`

var (
	reEmail  = regexp.MustCompile(`[\w.+%-]+@[\w-]+(?:\.[\w-]+)+`)
	reIBAN   = regexp.MustCompile(`(?i)\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,3})?\b`)
	reCF     = regexp.MustCompile(`(?i)\b[A-Z]{3} ?[A-Z]{3} ?\d{2} ?[A-EHLMPRST] ?\d{2} ?[A-Z] ?\d{3} ?[A-Z]\b`)
	reCard   = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	reIntl   = regexp.MustCompile(pre + `(\+\d{1,3}[\s.-]?\d(?:[\s.-]?\d){7,11})`)
	reMobPfx = regexp.MustCompile(pre + `((?:\+|00)39[\s.-]?3\d{2}[\s.-]?\d{3}[\s.-]?\d{3,4})\b`)
	reMob    = regexp.MustCompile(pre + `(3\d{2}[\s.-]?\d{3}[\s.-]?\d{4}|3\d{2}[\s.-]\d{3}[\s.-]\d{3})\b`)
	reLand   = regexp.MustCompile(pre + `(0\d{1,3}[\s./-]\d{5,8})\b`)
	// Loose forms: any grouping of the digits (spaces, dots, dashes, an
	// optional ")" after the area code). Validated by digit count in Text.
	reMobLoose  = regexp.MustCompile(pre + `(3\d{2}(?:[\s.-]?\d){7})\b`)
	reLandLoose = regexp.MustCompile(pre + `(\(?0\d{1,3}\)?(?:[\s-]?\d){5,8})\b`)
	reDateLike  = regexp.MustCompile(`^\(?\d{1,2}[-.]\d{1,2}[-.]\d{2,4}$`)
	reAddr      = regexp.MustCompile(`(?i)\b(?:via|viale|piazza|piazzale|corso|largo|vicolo|strada)\s+((?:(?:d['’]|dell['’]|dell[oa]?|dei|degli|delle|di|del)\s*)?[\p{L}'’.]+(?:\s+[\p{L}'’.]+){0,3}?),?\s*(?:n\.?|civico|nr\.?)?\s*\d+[a-z]?(?:/\w)?\b(?:,?\s*\d{5}(?:\s+\p{Lu}[\p{L}']+)?)?`)
	reOrder     = regexp.MustCompile(`\bF\d{9}\b`)
	reTag       = regexp.MustCompile(`(?s)<[^>]*>`)
	reBreak     = regexp.MustCompile(`(?i)<\s*(?:br\s*/?|/p|/div|/li|/h\d)\s*>`)
	reBlank     = regexp.MustCompile(`\n{3,}`)
)

// addrStop lists first words after "via" that mean "by way of", not a street.
var addrStop = map[string]bool{
	"mail": true, "email": true, "e-mail": true, "chat": true, "telefono": true,
	"whatsapp": true, "sms": true, "pec": true, "web": true, "internet": true,
	"posta": true, "app": true, "sito": true, "telefonica": true, "telematica": true,
}

// StripHTML converts simple HTML (bot messages) to plain text.
func StripHTML(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	s = reBreak.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	s = reBlank.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// Text masks personal data in s.
func Text(s string, o Options) string {
	s = reEmail.ReplaceAllString(s, "[EMAIL]")
	s = reIBAN.ReplaceAllStringFunc(s, func(m string) string {
		n := len(strings.ReplaceAll(m, " ", ""))
		if n < 15 || n > 34 {
			return m
		}
		return "[IBAN]"
	})
	s = reCF.ReplaceAllString(s, "[CF]")
	s = reCard.ReplaceAllStringFunc(s, func(m string) string {
		if luhn(digits(m)) {
			return "[CARD]"
		}
		return m
	})
	s = reMobPfx.ReplaceAllString(s, "${1}[PHONE]")
	s = reIntl.ReplaceAllString(s, "${1}[PHONE]")
	s = reMob.ReplaceAllString(s, "${1}[PHONE]")
	s = reLand.ReplaceAllString(s, "${1}[PHONE]")
	s = maskLoosePhones(s, reMobLoose, 10, 10)
	s = maskLoosePhones(s, reLandLoose, 9, 11)
	s = reAddr.ReplaceAllStringFunc(s, func(m string) string {
		sub := reAddr.FindStringSubmatch(m)
		first := strings.ToLower(strings.Fields(sub[1])[0])
		if addrStop[first] {
			return m
		}
		return "[ADDRESS]"
	})
	if o.MaskOrders {
		s = reOrder.ReplaceAllString(s, "[ORDER]")
	}
	return s
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func luhn(d string) bool {
	if len(d) < 13 || len(d) > 19 {
		return false
	}
	sum, alt := 0, false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

// maskLoosePhones replaces matches of re (group 1 = number) whose digit count
// is within [minD,maxD] and that do not look like a date.
func maskLoosePhones(s string, re *regexp.Regexp, minD, maxD int) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		sub := re.FindStringSubmatch(m)
		num := sub[2]
		if n := len(digits(num)); n < minD || n > maxD || reDateLike.MatchString(strings.TrimSpace(num)) {
			return m
		}
		return sub[1] + "[PHONE]"
	})
}
