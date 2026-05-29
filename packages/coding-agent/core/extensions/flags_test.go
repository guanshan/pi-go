package extensions

import "testing"

func TestAPIFlagRegistrationAndValues(t *testing.T) {
	api := NewAPI()
	api.RegisterFlag(FlagDefinition{Name: "preset", Description: "named preset", Type: "string", Default: "base"})

	// Default is seeded until a CLI value overrides it.
	if got := api.Flag("preset"); got != "base" {
		t.Fatalf("default flag value = %v, want base", got)
	}

	api.SetFlagValues(map[string]any{"preset": "fast", "unrelated": "x"})
	if got := api.Flag("preset"); got != "fast" {
		t.Fatalf("overridden flag value = %v, want fast", got)
	}
	// Values for flags no extension registered are not exposed (TS getFlag gate).
	if got := api.Flag("unrelated"); got != nil {
		t.Fatalf("unregistered flag = %v, want nil", got)
	}

	flags := api.SnapshotFlags()
	if len(flags) != 1 || flags[0].Name != "preset" || flags[0].Type != "string" {
		t.Fatalf("snapshot flags = %#v", flags)
	}
}

func TestRunnerFlagsThroughFactory(t *testing.T) {
	runner, err := NewRunner(func(api *API) error {
		api.RegisterFlag(FlagDefinition{Name: "verbose-x", Description: "extra logging", Type: "boolean", Default: false})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.SetFlagValues(map[string]any{"verbose-x": true})
	if got := runner.FlagValue("verbose-x"); got != true {
		t.Fatalf("flag value = %v, want true", got)
	}
	flags := runner.RegisteredFlags()
	if len(flags) != 1 || flags[0].Name != "verbose-x" {
		t.Fatalf("registered flags = %#v", flags)
	}
}
