package insights

import (
	"encoding/json"
	"testing"
)

func payloads(raw ...string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(raw))
	for _, r := range raw {
		out = append(out, json.RawMessage(r))
	}
	return out
}

func TestAggregateCostUsesCumulativeTotalsWithoutDoubleCounting(t *testing.T) {
	got := AggregateCost(payloads(
		`{"total_cost_usd":0.01}`, `{"total_cost_usd":0.025}`))
	if got == nil || got.TotalUSD != 0.025 || got.UpdateCount != 2 {
		t.Fatalf("cost = %+v", got)
	}
}

func TestAggregateCostAddsIncrements(t *testing.T) {
	got := AggregateCost(payloads(`{"cost_usd":0.01}`, `{"cost_usd":0.015}`))
	if got == nil || got.TotalUSD != 0.025 || got.UpdateCount != 2 {
		t.Fatalf("cost = %+v", got)
	}
}

func TestAggregateCostPrefersCumulativeOverIncrement(t *testing.T) {
	got := AggregateCost(payloads(
		`{"cost_usd":0.5}`, `{"total_cost_usd":0.04,"cost_usd":0.5}`))
	if got == nil || got.TotalUSD != 0.04 || got.UpdateCount != 2 {
		t.Fatalf("cost = %+v", got)
	}
}

func TestAggregateCostIgnoresMalformedPayloads(t *testing.T) {
	if got := AggregateCost(payloads(
		`{"cost_usd":-2}`, `{"total_cost_usd":"1"}`, `{}`, `not json`)); got != nil {
		t.Fatalf("cost = %+v, want nil for no valid updates", got)
	}
	got := AggregateCost(payloads(`{"cost_usd":-2}`, `{"cost_usd":0.5}`))
	if got == nil || got.TotalUSD != 0.5 || got.UpdateCount != 1 {
		t.Fatalf("cost = %+v", got)
	}
}

func TestAggregateCostEmpty(t *testing.T) {
	if got := AggregateCost(nil); got != nil {
		t.Fatalf("cost = %+v, want nil", got)
	}
}
