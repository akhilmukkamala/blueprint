package cli

// Tests for the verify feature's command registration (this file's sibling
// verifycmds.go only — never another feature's commands).

import "testing"

func TestVerifyCommandsRegistered(t *testing.T) {
	found := map[string]bool{}
	for _, c := range All() {
		found[c.Name()] = true
	}
	for _, name := range []string{"verify", "approve"} {
		if !found[name] {
			t.Errorf("command %q not registered", name)
		}
	}
}

func TestApproveHasAmendFlag(t *testing.T) {
	c := newApproveCmd()
	f := c.Flags().Lookup("amend")
	if f == nil {
		t.Fatal("approve must expose --amend (the logged re-stamp path)")
	}
	if f.DefValue != "false" {
		t.Fatalf("--amend default = %s, want false", f.DefValue)
	}
}

func TestCommandsRequireExactlyOneArg(t *testing.T) {
	v := newVerifyCmd()
	if err := v.Args(v, []string{}); err == nil {
		t.Error("verify should reject zero args")
	}
	if err := v.Args(v, []string{"id"}); err != nil {
		t.Errorf("verify should accept one arg: %v", err)
	}
	a := newApproveCmd()
	if err := a.Args(a, []string{"a", "b"}); err == nil {
		t.Error("approve should reject two args")
	}
}
