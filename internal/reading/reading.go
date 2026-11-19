// Package reading turns an OCR result into something the Bosch Therm 5600 F
// display can show. Port of display_reading.py; the lists come from the
// heater's manual (table 17 for errors, Fig. 7 and sections 4.6/8 for menus).
package reading

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MinScore is RapidOCR's confidence below which a reading is noise.
const MinScore = 0.7

var ErrorCodes = setOf("A0", "A7", "C7", "CA", "CF", "E1", "E2", "E9",
	"EA", "EC", "EE", "EF", "F7", "FA")

var MenuCodes = func() map[string]bool {
	m := setOf("P4", "P9", "SA", "PP", "DH", "OD", "FH", "LP", "HP",
		"H0", "H1", "H2", "C0", "C1", "C2",
		"P8", "P6", "P5", "P3", "AA", "AP", "P0", "L0", "ON", "OF", "00")
	for n := 1; n <= 10; n++ {
		m[strconv.Itoa(n)+"F"] = true // 1F..10F: the last 10 errors
	}
	for n := 1; n <= 5; n++ {
		m[strconv.Itoa(n)+"D"] = true // 1D..5D: operating data
	}
	return m
}()

// \p{Nd}, not \d: Python's \d matches every Unicode decimal digit.
var temperature = regexp.MustCompile(`^(\p{Nd}{2})(?:°?[CF])?$`)

func setOf(codes ...string) map[string]bool {
	m := make(map[string]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m
}

// Classify returns kind ("temperature", "error", "menu" or "none") and the
// cleaned value ("45", "E1", "P4", or "" for none).
func Classify(text string, score float64) (kind, value string) {
	if score < MinScore {
		return "none", ""
	}
	compact := strings.ToUpper(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text))
	if compact == "" {
		return "none", ""
	}
	// Menus first: "00" (P5's ignition delay) would otherwise pass as a temperature.
	if MenuCodes[compact] {
		return "menu", compact
	}
	if ErrorCodes[compact] {
		return "error", compact
	}
	if m := temperature.FindStringSubmatch(compact); m != nil {
		return "temperature", m[1]
	}
	return "none", ""
}
