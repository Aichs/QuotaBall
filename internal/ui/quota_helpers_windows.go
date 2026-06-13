//go:build windows

package ui

import "math"

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func quotaPercent(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	v := math.Round((used/limit*100)*10) / 10
	return math.Max(0, math.Min(100, v))
}
