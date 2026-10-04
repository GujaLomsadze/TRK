package derive

// Context bands (user requirement): ≥40 yellow, ≥60 red; hints at 50 and 70.
const (
	CtxYellow   = 40.0
	CtxHandoff  = 50.0
	CtxRed      = 60.0
	CtxReground = 70.0

	HintHandoff  = "Start thinking about handing off / compacting"
	HintReground = "Don't trust architectural reasoning without re-grounding it"
)

type CtxBand struct {
	Level string   `json:"level"` // none|ok|yellow|red
	Hints []string `json:"hints,omitempty"`
}

func CtxAdvice(pct *float64) CtxBand {
	if pct == nil {
		return CtxBand{Level: "none"}
	}
	p := *pct
	b := CtxBand{Level: "ok"}
	if p >= CtxYellow {
		b.Level = "yellow"
	}
	if p >= CtxRed {
		b.Level = "red"
	}
	if p >= CtxHandoff {
		b.Hints = append(b.Hints, HintHandoff)
	}
	if p >= CtxReground {
		b.Hints = append(b.Hints, HintReground)
	}
	return b
}
