package conflict

import (
	"context"
	"regexp"
	"sort"
)

var fakeContractPattern = regexp.MustCompile(`semantic-contract:\s*([A-Za-z0-9_.-]+)\s*=\s*([A-Za-z0-9_.-]+)`)

type FakeSemantic struct{}

func (FakeSemantic) Assess(_ context.Context, req SemanticRequest) (SemanticVerdict, error) {
	a := fakeContracts(req.TaskDiff)
	b := fakeContracts(req.OtherTaskDiff)
	keys := make([]string, 0)
	for key, av := range a {
		if bv, ok := b[key]; ok && av != bv {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return SemanticVerdict{}, nil
	}
	sort.Strings(keys)
	key := keys[0]
	return SemanticVerdict{
		Conflicts:   true,
		Severity:    SeverityHigh,
		Explanation: "Both changes assign incompatible behavior to " + key + ".",
		Evidence:    []string{key + "=" + a[key], key + "=" + b[key]},
	}, nil
}

func fakeContracts(diff string) map[string]string {
	out := map[string]string{}
	for _, match := range fakeContractPattern.FindAllStringSubmatch(diff, -1) {
		out[match[1]] = match[2]
	}
	return out
}
