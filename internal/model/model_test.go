package model

import "testing"

func TestKindForHook(t *testing.T) {
	cases := map[string]string{
		"PreToolUse": KindToolPre, "PostToolUseFailure": KindToolFail,
		"PermissionRequest": KindPermission, "Stop": KindStop, "Whatever": KindHookOther, "": KindHookOther,
	}
	for in, want := range cases {
		if got := KindForHook(in); got != want {
			t.Errorf("KindForHook(%q) = %q, want %q", in, got, want)
		}
	}
	for _, ev := range HookEvents {
		if KindForHook(ev) == KindHookOther {
			t.Errorf("registered hook %q has no kind", ev)
		}
	}
}
