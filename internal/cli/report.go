package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/client"
	"github.com/GujaLomsadze/trk/internal/model"
	"github.com/GujaLomsadze/trk/internal/ptree"
)

// Seams for tests.
var (
	send       = func(e model.Envelope) bool { return client.Default().Send(e) }
	findClaude = func() int { return ptree.FindClaude(os.Getppid()) }
	nowMS      = func() int64 { return time.Now().UnixMilli() }
)

type cliPayload struct {
	Text  string `json:"text,omitempty"`
	Steps int    `json:"steps,omitempty"`
	I     int    `json:"i,omitempty"`
	N     int    `json:"n,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
}

func envelope(source, kind string, payload json.RawMessage) model.Envelope {
	env := model.Envelope{TS: nowMS(), Source: source, Kind: kind, ClaudePID: findClaude(), Payload: payload}
	if s := os.Getenv("TRK_SESSION"); s != "" {
		env.SessionID, env.SessionVia = s, model.AttrEnv
	}
	return env
}

func report(kind string, args []string, stderr io.Writer) int {
	p := cliPayload{}
	p.Cwd, _ = os.Getwd()
	switch kind {
	case model.KindStart:
		p.Text, p.Steps = parseStart(args)
		if p.Text == "" {
			fmt.Fprintln(stderr, `usage: trk start "<task>" [--steps N]`)
			return 0
		}
	case model.KindStep, model.KindBlocked:
		p.Text = strings.TrimSpace(strings.Join(args, " "))
		if p.Text == "" {
			fmt.Fprintf(stderr, "usage: trk %s \"<text>\"\n", kind)
			return 0
		}
	case model.KindDone:
		p.Text = strings.TrimSpace(strings.Join(args, " "))
	case model.KindProgress:
		i, n, ok := parseProgress(strings.Join(args, ""))
		if !ok {
			fmt.Fprintln(stderr, "usage: trk progress <i>/<N>")
			return 0
		}
		p.I, p.N = i, n
	}
	b, _ := json.Marshal(p)
	send(envelope(model.SourceCLI, kind, b))
	return 0
}

func parseStart(args []string) (string, int) {
	var words []string
	steps := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--steps" && i+1 < len(args):
			steps, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--steps="):
			steps, _ = strconv.Atoi(strings.TrimPrefix(a, "--steps="))
		default:
			words = append(words, a)
		}
	}
	if steps < 0 {
		steps = 0
	}
	return strings.TrimSpace(strings.Join(words, " ")), steps
}

func parseProgress(s string) (int, int, bool) {
	s = strings.ReplaceAll(s, " ", "")
	is, ns, hasN := strings.Cut(s, "/")
	i, err := strconv.Atoi(is)
	if err != nil || i < 0 {
		return 0, 0, false
	}
	if !hasN {
		return i, 0, true
	}
	n, err := strconv.Atoi(ns)
	if err != nil || n < 0 {
		return 0, 0, false
	}
	return i, n, true
}
