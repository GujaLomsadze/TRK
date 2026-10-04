package fleet

import (
	"regexp"
	"strings"
)

const cardTextMax = 400 // runes kept for the last prompt / last reply

var (
	pastedRe = regexp.MustCompile(`(?s)<pasted_content[^>]*>.*?</pasted_content[^>]*>`)
	mdMarks  = strings.NewReplacer("**", "", "__", "", "`", "")
)

// cardText flattens a prompt or reply for a card: pasted blocks become a marker,
// whitespace collapses to single spaces, markdown emphasis goes (md), and long text is cut.
func cardText(s string, md bool) string {
	s = pastedRe.ReplaceAllString(s, " [pasted text] ")
	if md {
		s = mdMarks.Replace(s)
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > cardTextMax {
		s = string(r[:cardTextMax-1]) + "…"
	}
	return s
}

// countSubagent records a finished subagent once per id. The map is replaced, not
// mutated, because views hand the session (and its map) to the JSON encoder unlocked.
func (e *entry) countSubagent(p payload) {
	if p.AgentID != "" {
		if e.subSeen[p.AgentID] {
			return
		}
		if e.subSeen == nil {
			e.subSeen = map[string]bool{}
		}
		e.subSeen[p.AgentID] = true
	}
	typ := p.AgentType
	if typ == "" {
		typ = "agent"
	}
	next := make(map[string]int, len(e.S.Subagents)+1)
	for k, v := range e.S.Subagents {
		next[k] = v
	}
	next[typ]++
	e.S.Subagents = next
}
