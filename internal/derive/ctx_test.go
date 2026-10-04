package derive

import (
	"reflect"
	"testing"
)

func f(v float64) *float64 { return &v }

func TestCtxAdvice(t *testing.T) {
	cases := []struct {
		pct   *float64
		level string
		hints []string
	}{
		{nil, "none", nil},
		{f(0), "ok", nil},
		{f(39.9), "ok", nil},
		{f(40), "yellow", nil},
		{f(49.9), "yellow", nil},
		{f(50), "yellow", []string{HintHandoff}},
		{f(59.9), "yellow", []string{HintHandoff}},
		{f(60), "red", []string{HintHandoff}},
		{f(69.9), "red", []string{HintHandoff}},
		{f(70), "red", []string{HintHandoff, HintReground}},
		{f(98), "red", []string{HintHandoff, HintReground}},
	}
	for _, c := range cases {
		got := CtxAdvice(c.pct)
		if got.Level != c.level || !reflect.DeepEqual(got.Hints, c.hints) {
			t.Errorf("CtxAdvice(%v) = %+v, want %s %v", c.pct, got, c.level, c.hints)
		}
	}
}
