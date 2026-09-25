package insights

import (
	"encoding/json"
	"math"
)

// Cumulative costs replace totals; increments add in event order; nil when nothing was reported.
func AggregateCost(payloads []json.RawMessage) *CostSummary {
	var summary CostSummary
	for _, raw := range payloads {
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}
		cumulative, hasCumulative := nonNegativeNumber(payload["total_cost_usd"])
		increment, hasIncrement := nonNegativeNumber(payload["cost_usd"])
		if !hasCumulative && !hasIncrement {
			continue
		}
		if hasCumulative {
			summary.TotalUSD = cumulative
		} else {
			summary.TotalUSD += increment
		}
		summary.UpdateCount++
	}
	if summary.UpdateCount == 0 {
		return nil
	}
	return &summary
}

func nonNegativeNumber(value any) (float64, bool) {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, false
	}
	return number, true
}
