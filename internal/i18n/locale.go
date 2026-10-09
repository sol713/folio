// Package i18n localizes human-facing text while leaving protocol identifiers intact.
package i18n

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const Chinese = "zh-CN"
const English = "en"

// Resolve defaults to Simplified Chinese. Locale aliases never alter machine keys.
func Resolve(value string) string {
	if locale, ok := Supported(value); ok {
		return locale
	}
	return Chinese
}
func Supported(value string) (string, bool) {
	v := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
	switch {
	case v == "zh" || v == "zh-cn" || v == "zh-hans" || strings.HasPrefix(v, "zh-hans-"):
		return Chinese, true
	case v == "en" || strings.HasPrefix(v, "en-"):
		return English, true
	}
	return "", false
}
func AcceptLanguage(value string) string {
	type choice struct {
		locale string
		q      float64
		index  int
	}
	choices := []choice{}
	for index, part := range strings.Split(value, ",") {
		items := strings.Split(strings.TrimSpace(part), ";")
		locale, ok := Supported(items[0])
		if !ok {
			continue
		}
		q := 1.0
		for _, p := range items[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "q=") {
				n, e := strconv.ParseFloat(strings.TrimPrefix(p, "q="), 64)
				if e != nil || n < 0 || n > 1 {
					q = 0
				} else {
					q = n
				}
			}
		}
		if q > 0 {
			choices = append(choices, choice{locale, q, index})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].q > choices[j].q })
	if len(choices) > 0 {
		return choices[0].locale
	}
	return Chinese
}
func CookieLocale(r *http.Request) string {
	if c, e := r.Cookie("folio.locale"); e == nil {
		if locale, ok := Supported(c.Value); ok {
			return locale
		}
	}
	return Chinese
}
func RequestLocale(r *http.Request) string {
	if value := r.Header.Get("X-Folio-Lang"); value != "" {
		return Resolve(value)
	}
	if value := r.Header.Get("Accept-Language"); value != "" {
		return AcceptLanguage(value)
	}
	return CookieLocale(r)
}
func Message(locale, english string) string {
	if Resolve(locale) == English {
		return english
	}
	if translated, ok := messages[english]; ok {
		return translated
	}
	return english
}
func Format(locale, template string, args ...any) string {
	return fmt.Sprintf(Message(locale, template), args...)
}
func Known(english string) bool { _, ok := messages[english]; return ok }
