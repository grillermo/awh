package reading

import (
	"strings"
	"testing"
)

const sure = 0.95

func check(t *testing.T, text string, score float64, kind, value string) {
	t.Helper()
	k, v := Classify(text, score)
	if k != kind || v != value {
		t.Errorf("Classify(%q, %v) = (%q, %q), want (%q, %q)", text, score, k, v, kind, value)
	}
}

func TestTemperatures(t *testing.T) {
	check(t, "45", sure, "temperature", "45")
	check(t, "45°C", sure, "temperature", "45")
	check(t, "45C", sure, "temperature", "45")
	check(t, " 60 ", sure, "temperature", "60")
}

func TestEveryManualErrorCodeIsAnError(t *testing.T) {
	for code := range ErrorCodes {
		check(t, code, sure, "error", code)
	}
	check(t, "e1", sure, "error", "E1")
}

func TestMenuCodesAreNotErrors(t *testing.T) {
	for _, code := range []string{"P4", "SA", "oF", "on", "10F", "3D", "C1", "00"} {
		check(t, code, sure, "menu", strings.ToUpper(code))
	}
}

func TestLowConfidenceIsNothing(t *testing.T) {
	check(t, "45", MinScore-0.01, "none", "")
	check(t, "E1", 0.3, "none", "")
	check(t, "45", 0, "none", "")
}

func TestAnythingNotInTheManualIsNothing(t *testing.T) {
	for _, text := range []string{"恒", "福", "鸭", "C", "1", "8", "188", "451", "4", "UG", "W", ""} {
		check(t, text, sure, "none", "")
	}
}
