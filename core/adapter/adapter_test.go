package adapter_test

import (
	"errors"
	"testing"
	"time"

	"github.com/provehito-project/provehito/core/adapter"
	"github.com/provehito-project/provehito/core/failure"
)

func TestCostRankForMapsTheDeclaredVocabulary(t *testing.T) {
	cases := map[string]struct {
		rank int
		ok   bool
	}{
		"economy": {0, true}, "cheap": {0, true}, "low": {0, true},
		"standard": {1, true}, "medium": {1, true}, "ECONOMY": {0, true},
		"premium": {2, true}, "high": {2, true},
		"": {0, false}, "unknown": {0, false},
	}
	for class, want := range cases {
		rank, ok := adapter.CostRankFor(class)
		if rank != want.rank || ok != want.ok {
			t.Errorf("class %q: got (%d, %t)", class, rank, ok)
		}
	}
}

func envelope() adapter.DispatchEnvelope {
	return adapter.DispatchEnvelope{
		Adapter: "local", Family: "family-a",
		MaxSeconds: 5, MaxOutputBytes: 4096, CostClass: "economy",
	}
}

func dispatchProfile() adapter.Profile {
	return adapter.Profile{
		ID: "local", Executable: "/bin/true", Family: "family-a",
		Capabilities: []string{"writer"}, Timeout: 5 * time.Second, OutputLimit: 4096,
	}
}

func TestValidateDispatchAcceptsFittingProfile(t *testing.T) {
	if err := adapter.ValidateDispatch(dispatchProfile(), "economy", envelope()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDispatchRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*adapter.Profile, *string, *adapter.DispatchEnvelope)
		code   int
		op     string
	}{
		{"adapter identity mismatch", func(p *adapter.Profile, c *string, e *adapter.DispatchEnvelope) { p.ID = "other" }, 20, "agent profile dispatch mismatch"},
		{"family identity mismatch", func(p *adapter.Profile, c *string, e *adapter.DispatchEnvelope) { p.Family = "family-b" }, 20, "agent profile dispatch mismatch"},
		{"seconds over limit", func(p *adapter.Profile, c *string, e *adapter.DispatchEnvelope) { p.Timeout = 6 * time.Second }, 20, "agent profile exceeds dispatch limits"},
		{"sub-second over limit", func(p *adapter.Profile, c *string, e *adapter.DispatchEnvelope) {
			p.Timeout = 5*time.Second + time.Millisecond
		}, 20, "agent profile exceeds dispatch limits"},
		{"dispatch seconds unset", func(p *adapter.Profile, c *string, e *adapter.DispatchEnvelope) { e.MaxSeconds = 0 }, 20, "agent profile exceeds dispatch limits"},
		{"dispatch output unset", func(p *adapter.Profile, c *string, e *adapter.DispatchEnvelope) { e.MaxOutputBytes = 0 }, 20, "agent profile exceeds dispatch limits"},
		{"output over limit", func(p *adapter.Profile, c *string, e *adapter.DispatchEnvelope) { p.OutputLimit = 4097 }, 20, "agent profile exceeds dispatch limits"},
		{"cost class mismatch", func(p *adapter.Profile, c *string, e *adapter.DispatchEnvelope) { *c = "premium" }, 20, "agent cost class mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile, class, env := dispatchProfile(), "economy", envelope()
			tc.mutate(&profile, &class, &env)
			err := adapter.ValidateDispatch(profile, class, env)
			if failure.ExitCodeFor(err) != tc.code {
				t.Fatalf("got %v", err)
			}
			var classified *failure.Error
			if !errors.As(err, &classified) || classified.Op != tc.op {
				t.Fatalf("op: got %v", err)
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
