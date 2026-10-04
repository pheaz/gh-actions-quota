package quota

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

const DefaultThreshold = 50

func IncludedMinutesForPlan(plan string) (int, error) {
	minutes := map[string]int{
		"free": 2000, "pro": 3000, "team": 3000,
		"enterprise": 50000, "enterprise cloud": 50000,
	}[strings.ToLower(strings.TrimSpace(plan))]
	if minutes == 0 {
		return 0, errors.New("unsupported GitHub plan; set quota-minutes explicitly")
	}
	return minutes, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func ParsePositive(value string) (float64, error) {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || !finite(number) || number <= 0 {
		return 0, errors.New("quota-minutes must be positive and finite")
	}
	return number, nil
}

func ParseThreshold(value string) (float64, error) {
	if value == "" {
		return DefaultThreshold, nil
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || !finite(number) || number <= 0 || number > 100 {
		return 0, errors.New("threshold must be greater than 0 and at most 100")
	}
	return number, nil
}

type Usage struct {
	UsedMinutes      float64
	QuotaMinutes     float64
	RemainingMinutes float64
	UsagePercent     float64
	Allowed          bool
}

func Calculate(used, quota, threshold float64) (Usage, error) {
	if !finite(used) || used < 0 || !finite(quota) || quota <= 0 || !finite(threshold) || threshold <= 0 || threshold > 100 {
		return Usage{}, errors.New("invalid quota calculation inputs")
	}
	percent := used / quota * 100
	if !finite(percent) {
		return Usage{}, errors.New("GitHub billing usage is out of range")
	}
	return Usage{
		UsedMinutes: used, QuotaMinutes: quota,
		RemainingMinutes: math.Max(0, quota-used), UsagePercent: percent,
		Allowed: percent < threshold,
	}, nil
}
