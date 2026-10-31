package config

import "testing"

// Oracle: chief's operator-account requirements, not operator_account.go's
// implementation. "First configured" means the first eligible human username
// in configured order: skip empty usernames and the machine principal "cli".
// No trimming or case-folding behavior is specified, so none is invented here.
// These are real pure-helper calls, with no mocks or filesystem dependencies.
//
// CHECK, independently of this author, must test mutations such as selecting
// the last account, returning "cli", or ignoring a signed-in person's actor.
// Pre-change RED and mutation proof for already-implemented helpers remain
// unverified until that independent CHECK; never manufacture a failing oracle.

func operatorAccountTestConfig(usernames []string) *Config {
	cfg := &Config{}
	if usernames != nil {
		cfg.Gateway.Users = make([]UserConfig, len(usernames))
		for i, username := range usernames {
			cfg.Gateway.Users[i] = UserConfig{Username: username}
		}
	}
	return cfg
}

func TestOperatorUsername_FirstConfiguredHumanWins(t *testing.T) {
	for _, tc := range []struct {
		name      string
		usernames []string
		nilConfig bool
		want      string
	}{
		{name: "nil_config", nilConfig: true, want: ""},
		{name: "no_accounts_nil_slice", usernames: nil, want: ""},
		{name: "no_accounts_empty_slice", usernames: []string{}, want: ""},
		{name: "one_human_boundary", usernames: []string{"alice"}, want: "alice"},
		{name: "configured_order_not_alphabetical", usernames: []string{"zoe", "alice"}, want: "zoe"},
		{name: "reversed_configured_order", usernames: []string{"alice", "zoe"}, want: "alice"},
		{name: "empty_username_only", usernames: []string{""}, want: ""},
		{name: "all_usernames_empty", usernames: []string{"", ""}, want: ""},
		{name: "skip_empty_before_human", usernames: []string{"", "alice", "bob"}, want: "alice"},
		{name: "machine_principal_only", usernames: []string{"cli"}, want: ""},
		{name: "machine_principal_before_human", usernames: []string{"cli", "alice", "bob"}, want: "alice"},
		{name: "empty_and_machine_before_human", usernames: []string{"", "cli", "", "zoe", "alice"}, want: "zoe"},
		{name: "only_ineligible_accounts", usernames: []string{"", "cli", ""}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg *Config
			if !tc.nilConfig {
				cfg = operatorAccountTestConfig(tc.usernames)
			}
			if got := cfg.OperatorUsername(); got != tc.want {
				t.Errorf("OperatorUsername() = %q, want %q for configured usernames %q", got, tc.want, tc.usernames)
			}
		})
	}
}

func TestMainOwnerFor_PersonWinsAndMachineFallsBackToOperator(t *testing.T) {
	for _, tc := range []struct {
		name      string
		usernames []string
		actor     string
		want      string
	}{
		{name: "person_overrides_operator", usernames: []string{"alice", "bob"}, actor: "bob", want: "bob"},
		{name: "person_can_equal_operator", usernames: []string{"alice"}, actor: "alice", want: "alice"},
		{name: "person_without_configured_accounts", usernames: nil, actor: "alice", want: "alice"},
		{name: "person_not_selected_by_account_order", usernames: []string{"zoe", "alice"}, actor: "bob", want: "bob"},
		{name: "system_actor_uses_first_account", usernames: []string{"zoe", "alice"}, actor: "", want: "zoe"},
		{name: "machine_actor_uses_operator", usernames: []string{"alice", "bob"}, actor: "cli", want: "alice"},
		{name: "system_actor_skips_empty_username", usernames: []string{"", "alice", "bob"}, actor: "", want: "alice"},
		{name: "system_actor_skips_machine_account", usernames: []string{"cli", "alice"}, actor: "", want: "alice"},
		{name: "machine_actor_skips_machine_account", usernames: []string{"cli", "alice"}, actor: "cli", want: "alice"},
		{name: "person_overrides_machine_only_config", usernames: []string{"cli"}, actor: "bob", want: "bob"},
		{name: "no_accounts_system_actor", usernames: nil, actor: "", want: ""},
		{name: "no_accounts_machine_actor", usernames: []string{}, actor: "cli", want: ""},
		{name: "empty_account_system_actor", usernames: []string{""}, actor: "", want: ""},
		{name: "machine_only_config_system_actor", usernames: []string{"cli"}, actor: "", want: ""},
		{name: "machine_only_config_machine_actor", usernames: []string{"cli"}, actor: "cli", want: ""},
		{name: "only_ineligible_accounts_machine_actor", usernames: []string{"", "cli", ""}, actor: "cli", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := operatorAccountTestConfig(tc.usernames)
			if got := cfg.MainOwnerFor(tc.actor); got != tc.want {
				t.Errorf("MainOwnerFor(%q) = %q, want %q for configured usernames %q", tc.actor, got, tc.want, tc.usernames)
			}
		})
	}
}
