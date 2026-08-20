// Package adapter defines the trusted, explicit local-process launch profile.
package adapter

import (
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/provehito-project/provehito/core/failure"
)

// Profile is a static local-process launch profile. Arguments are passed
// directly to exec; they are never interpreted as shell text.
type Profile struct {
	ID               string
	Executable       string
	Args             []string
	Capabilities     []string
	Family           string
	CostRank         int
	EnvAllowlist     []string
	VersionProbeArgs []string
	Timeout          time.Duration
	OutputLimit      int64
}

// Requirement describes the capability and optional family exclusion needed
// by a launch.
type Requirement struct {
	Capability     string
	ExcludedFamily string
}

// Validate checks every launch-profile field and rejects ambiguous values.
func Validate(profile Profile) error {
	if err := validateRouting(profile, true); err != nil {
		return err
	}
	if profile.Executable == "" || !filepath.IsAbs(profile.Executable) || strings.IndexByte(profile.Executable, 0) >= 0 {
		return failure.New(failure.UsageOrSchema, "adapter executable")
	}
	if profile.Timeout <= 0 {
		return failure.New(failure.UsageOrSchema, "adapter timeout")
	}
	if profile.OutputLimit <= 0 {
		return failure.New(failure.UsageOrSchema, "adapter output limit")
	}
	if err := validateArguments(profile.Args, "adapter arguments"); err != nil {
		return err
	}
	if err := validateArguments(profile.VersionProbeArgs, "adapter version probe arguments"); err != nil {
		return err
	}
	if err := validateEnvironment(profile.EnvAllowlist); err != nil {
		return err
	}
	return nil
}

func validateRouting(profile Profile, requireFamily bool) error {
	if profile.ID == "" || strings.IndexByte(profile.ID, 0) >= 0 {
		return failure.New(failure.UsageOrSchema, "adapter profile id")
	}
	if profile.CostRank < 0 {
		return failure.New(failure.UsageOrSchema, "adapter cost rank")
	}
	if requireFamily && (profile.Family == "" || strings.IndexByte(profile.Family, 0) >= 0) {
		return failure.New(failure.UsageOrSchema, "adapter family")
	}
	seen := make(map[string]struct{}, len(profile.Capabilities))
	if len(profile.Capabilities) == 0 {
		return failure.New(failure.UsageOrSchema, "adapter capabilities")
	}
	for _, capability := range profile.Capabilities {
		if capability == "" || strings.IndexByte(capability, 0) >= 0 {
			return failure.New(failure.UsageOrSchema, "adapter capability")
		}
		if _, ok := seen[capability]; ok {
			return failure.New(failure.UsageOrSchema, "duplicate adapter capability")
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func validateArguments(args []string, op string) error {
	for _, arg := range args {
		if strings.IndexByte(arg, 0) >= 0 {
			return failure.New(failure.UsageOrSchema, op)
		}
	}
	return nil
}

func validateEnvironment(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" || strings.IndexByte(name, '=') >= 0 || !validEnvironmentName(name) {
			return failure.New(failure.UsageOrSchema, "adapter environment allowlist")
		}
		if _, ok := seen[name]; ok {
			return failure.New(failure.UsageOrSchema, "duplicate adapter environment allowlist")
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validEnvironmentName(name string) bool {
	for index, r := range name {
		if (index == 0 && !(r == '_' || unicode.IsLetter(r))) || (index > 0 && !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r))) {
			return false
		}
	}
	return true
}

func hasCapability(profile Profile, capability string) bool {
	for _, candidate := range profile.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}
