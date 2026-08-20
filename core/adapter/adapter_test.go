package adapter_test

import (
	"testing"
	"time"

	"github.com/provehito-project/provehito/core/adapter"
)

func TestSelectCheapestEligible(t *testing.T) {
	profiles := []adapter.Profile{
		validProfile("premium", 30),
		validProfile("economy", 10),
	}
	got, err := adapter.SelectCheapest(profiles, adapter.Requirement{Capability: "writer"})
	if err != nil || got.ID != "economy" {
		t.Fatalf("got %#v err %v", got, err)
	}
}

func TestSelectCheapestUsesStableIDTieBreak(t *testing.T) {
	profiles := []adapter.Profile{validProfile("zeta", 10), validProfile("alpha", 10)}
	got, err := adapter.SelectCheapest(profiles, adapter.Requirement{Capability: "writer"})
	if err != nil || got.ID != "alpha" {
		t.Fatalf("got %#v err %v", got, err)
	}
}

func TestSelectCheapestRejectsCapabilityAndFamilyMismatch(t *testing.T) {
	profiles := []adapter.Profile{validProfile("writer", 1)}
	profiles[0].Family = "same"
	if _, err := adapter.SelectCheapest(profiles, adapter.Requirement{Capability: "reviewer"}); err == nil {
		t.Fatal("missing capability was accepted")
	}
	if _, err := adapter.SelectCheapest(profiles, adapter.Requirement{Capability: "writer", ExcludedFamily: "same"}); err == nil {
		t.Fatal("excluded family was accepted")
	}
}

func validProfile(id string, cost int) adapter.Profile {
	return adapter.Profile{
		ID: id, Executable: "/bin/true", Family: "local", CostRank: cost,
		Capabilities: []string{"writer"}, Timeout: time.Second, OutputLimit: 128,
	}
}

func TestSelectCheapestRejectsInvalidLaunchProfile(t *testing.T) {
	cases := map[string]func(*adapter.Profile){
		"relative executable": func(profile *adapter.Profile) { profile.Executable = "true" },
		"bad environment":     func(profile *adapter.Profile) { profile.EnvAllowlist = []string{"BAD=NAME"} },
		"zero timeout":        func(profile *adapter.Profile) { profile.Timeout = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			invalid := validProfile("invalid", 1)
			mutate(&invalid)
			valid := validProfile("valid", 2)
			if _, err := adapter.SelectCheapest([]adapter.Profile{invalid, valid}, adapter.Requirement{Capability: "writer"}); err == nil {
				t.Fatal("invalid launch profile was accepted")
			}
		})
	}
}

func TestInvalidProfilesAreRejected(t *testing.T) {
	for name, profile := range map[string]adapter.Profile{
		"missing id":         {Capabilities: []string{"writer"}},
		"missing capability": {ID: "x", Capabilities: []string{""}},
		"negative cost":      {ID: "x", Capabilities: []string{"writer"}, CostRank: -1},
		"empty family":       {ID: "x", Family: "", Capabilities: []string{"writer"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := adapter.Validate(profile); err == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
}

func TestInvalidLaunchFieldsAreRejected(t *testing.T) {
	base := adapter.Profile{
		ID: "agent", Executable: "/bin/true", Family: "local", Capabilities: []string{"writer"},
		Timeout: 1, OutputLimit: 1,
	}
	nulExecutable := base
	nulExecutable.Executable = "/bin/true\x00bad"
	nulFixedArgument := base
	nulFixedArgument.Args = []string{"literal\x00bad"}
	badEnvironment := base
	badEnvironment.EnvAllowlist = []string{"NOT=NAME"}
	zeroTimeout := base
	zeroTimeout.Timeout = 0
	zeroOutputLimit := base
	zeroOutputLimit.OutputLimit = 0
	relativeExecutable := base
	relativeExecutable.Executable = "true"
	cases := map[string]adapter.Profile{
		"nul executable":      nulExecutable,
		"nul fixed argument":  nulFixedArgument,
		"bad environment":     badEnvironment,
		"zero timeout":        zeroTimeout,
		"zero output limit":   zeroOutputLimit,
		"relative executable": relativeExecutable,
	}
	for name, profile := range cases {
		t.Run(name, func(t *testing.T) {
			if err := adapter.Validate(profile); err == nil {
				t.Fatal("invalid launch field accepted")
			}
		})
	}
}
