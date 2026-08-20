package adapter

import (
	"sort"
	"strings"

	"github.com/provehito-project/provehito/core/failure"
)

// SelectCheapest returns the eligible profile with the lowest cost rank, then
// the lexicographically smallest ID for a deterministic tie break.
func SelectCheapest(profiles []Profile, requirement Requirement) (Profile, error) {
	if requirement.Capability == "" || strings.IndexByte(requirement.Capability, 0) >= 0 || strings.IndexByte(requirement.ExcludedFamily, 0) >= 0 {
		return Profile{}, failure.New(failure.UsageOrSchema, "adapter requirement capability")
	}
	if len(profiles) == 0 {
		return Profile{}, failure.New(failure.PolicyOrTransition, "no eligible adapter profile")
	}
	seen := make(map[string]struct{}, len(profiles))
	eligible := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		if err := Validate(profile); err != nil {
			return Profile{}, err
		}
		if _, exists := seen[profile.ID]; exists {
			return Profile{}, failure.New(failure.UsageOrSchema, "duplicate adapter profile id")
		}
		seen[profile.ID] = struct{}{}
		if !hasCapability(profile, requirement.Capability) || (requirement.ExcludedFamily != "" && profile.Family == requirement.ExcludedFamily) {
			continue
		}
		eligible = append(eligible, profile)
	}
	if len(eligible) == 0 {
		return Profile{}, failure.New(failure.PolicyOrTransition, "no eligible adapter profile")
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].CostRank != eligible[j].CostRank {
			return eligible[i].CostRank < eligible[j].CostRank
		}
		return eligible[i].ID < eligible[j].ID
	})
	return eligible[0], nil
}
