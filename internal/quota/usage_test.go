package quota

import (
	"math"
	"testing"
)

func TestPlanMapping(t *testing.T) {
	for plan, want := range map[string]int{"free": 2000, "PRO": 3000, "team": 3000, "enterprise": 50000, " Enterprise Cloud ": 50000} {
		if got, err := IncludedMinutesForPlan(plan); err != nil || got != want {
			t.Fatalf("plan %s: got %d, err %v, want %d", plan, got, err, want)
		}
	}
	if _, err := IncludedMinutesForPlan("unknown"); err == nil {
		t.Fatal("unknown plan accepted")
	}
}

func TestCalculateThresholdAndRemaining(t *testing.T) {
	for _, used := range []float64{1499, 1500, 1501, 4000} {
		usage, err := Calculate(used, 3000, 50)
		if err != nil || usage.Allowed != (used < 1500) || usage.RemainingMinutes != math.Max(0, 3000-used) {
			t.Fatalf("wrong usage: %+v err=%v", usage, err)
		}
	}
	// Display rounding must not control the gate just below the exact threshold.
	usage, err := Calculate(1499.99999999, 3000, 50)
	if err != nil || !usage.Allowed {
		t.Fatal("unrounded usage below threshold rejected")
	}
}

func TestInvalidCalculations(t *testing.T) {
	for _, args := range [][3]float64{
		{-1, 2000, 50}, {math.NaN(), 2000, 50}, {math.Inf(1), 2000, 50},
		{1, 0, 50}, {1, math.Inf(1), 50}, {1, 2000, 0}, {1, 2000, 101}, {1, 2000, math.NaN()},
		{math.MaxFloat64, math.SmallestNonzeroFloat64, 50},
	} {
		if _, err := Calculate(args[0], args[1], args[2]); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestInputParsing(t *testing.T) {
	if got, err := ParseThreshold(""); err != nil || got != DefaultThreshold {
		t.Fatal("wrong default threshold")
	}
	for _, text := range []string{"1", " 50 ", "100"} {
		if _, err := ParseThreshold(text); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"0", "-1", "101", "NaN", "Infinity", "invalid", " "} {
		if _, err := ParseThreshold(text); err == nil {
			t.Fatalf("invalid threshold accepted: %q", text)
		}
	}
	if got, err := ParsePositive(" 4000.5 "); err != nil || got != 4000.5 {
		t.Fatal("valid override rejected")
	}
	for _, text := range []string{"", "0", "-1", "NaN", "Infinity", "1e999", "invalid"} {
		if _, err := ParsePositive(text); err == nil {
			t.Fatalf("invalid quota accepted: %q", text)
		}
	}
}
