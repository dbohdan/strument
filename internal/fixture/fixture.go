// Package fixture implements the record/replay harness of
// fixture-harness-spec.md: the JSON-Lines scenario schema (§2), a loader that
// fails loudly on a version mismatch, and replay stubs for the coder's
// ModelClient / Confirmer / CommandRunner ports (§6).
package fixture

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/dbohdan/strument/internal/llm"
)

// Version is the fixture schema version checked on load.
const Version = 1

// Meta is the leading row of every scenario file.
type Meta struct {
	V        int    `json:"v"`
	Kind     string `json:"kind"`
	Scenario string `json:"scenario"`
	Source   string `json:"source"` // captured | authored | mutated
	Recorded string `json:"recorded,omitempty"`
	AiderSHA string `json:"aider_sha,omitempty"`
	Model    string `json:"model,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// FSEntry is a given or expected file state.
type FSEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// GitState describes the scenario's starting repository.
type GitState struct {
	Mode string `json:"mode"` // none | clean | dirty | commits:<n>
}

// Chat lists the files in the chat.
type Chat struct {
	Editable []string `json:"editable"`
	Readonly []string `json:"readonly"`
}

// Confirm is one scripted confirmation answer, consumed in file order.
type Confirm struct {
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
}

// Command is one scripted CommandRunner result, consumed in file order.
type Command struct {
	Block  string `json:"block"`
	Exit   int    `json:"exit"`
	Output string `json:"output"`
}

// Request is a captured provider request plus its assertion policy
// (fixture-harness §3).
type Request struct {
	Body            json.RawMessage `json:"body"`
	Assert          string          `json:"assert,omitempty"` // "" or "subset"
	Ignore          []string        `json:"ignore,omitempty"`
	KnownDivergence []string        `json:"known_divergence,omitempty"`
}

// Event is one fixture stream event. Kind "Error" rows carry Class/Message
// and are surfaced by the replay stub as *llm.StreamError, not as an event.
type Event struct {
	Kind         string     `json:"kind"`
	Text         string     `json:"text,omitempty"`
	Usage        *llm.Usage `json:"usage,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Class        string     `json:"class,omitempty"`
	Message      string     `json:"message,omitempty"`
}

// Turn is one request/stream pair. Multi-send scenarios repeat turns in
// order. Request may be nil for authored turns that assert nothing about the
// outgoing request.
type Turn struct {
	Request *Request
	Events  []Event
}

// ExpectOutcome asserts the send outcome and reflection count.
type ExpectOutcome struct {
	Outcome     string `json:"outcome"`
	Reflections int    `json:"reflections"`
}

// ExpectMessage asserts one history row.
type ExpectMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// ExpectUsage asserts accumulated usage for the scenario.
type ExpectUsage struct {
	Sent      int     `json:"sent"`
	Received  int     `json:"received"`
	CostKnown bool    `json:"cost_known"`
	USD       float64 `json:"usd,omitempty"`
}

// Scenario is a fully loaded fixture file.
type Scenario struct {
	Meta     Meta
	FS       []FSEntry
	Git      *GitState
	Config   json.RawMessage // raw "config" row, interpreted by the consumer
	Chat     *Chat
	User     string
	Confirms []Confirm
	Commands []Command
	Turns    []Turn

	ExpectFS      []FSEntry
	ExpectOutcome *ExpectOutcome
	ExpectHistory []ExpectMessage
	ExpectUsage   *ExpectUsage
}

// Load reads a scenario from path. See Read.
func Load(path string) (*Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc, err := Read(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return sc, nil
}

// Read parses a JSONL scenario. The first row must be a meta row with a
// matching schema version; unknown row kinds are errors (fail loudly).
func Read(r io.Reader) (*Scenario, error) {
	sc := &Scenario{}
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scan.Scan() {
		line := scan.Bytes()
		lineNo++
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if lineNo == 1 {
			if probe.Kind != "meta" {
				return nil, fmt.Errorf("line 1: first row must be meta, got %q", probe.Kind)
			}
		}
		switch probe.Kind {
		case "meta":
			if err := json.Unmarshal(line, &sc.Meta); err != nil {
				return nil, fmt.Errorf("line %d: meta: %w", lineNo, err)
			}
			if sc.Meta.V != Version {
				return nil, fmt.Errorf("line %d: fixture schema v%d, this build reads v%d", lineNo, sc.Meta.V, Version)
			}
		case "fs":
			var e FSEntry
			if err := json.Unmarshal(line, &e); err != nil {
				return nil, fmt.Errorf("line %d: fs: %w", lineNo, err)
			}
			sc.FS = append(sc.FS, e)
		case "git":
			var g GitState
			if err := json.Unmarshal(line, &g); err != nil {
				return nil, fmt.Errorf("line %d: git: %w", lineNo, err)
			}
			sc.Git = &g
		case "config":
			sc.Config = append(json.RawMessage(nil), line...)
		case "chat":
			var c Chat
			if err := json.Unmarshal(line, &c); err != nil {
				return nil, fmt.Errorf("line %d: chat: %w", lineNo, err)
			}
			sc.Chat = &c
		case "user":
			var u struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(line, &u); err != nil {
				return nil, fmt.Errorf("line %d: user: %w", lineNo, err)
			}
			sc.User = u.Text
		case "confirm":
			var c Confirm
			if err := json.Unmarshal(line, &c); err != nil {
				return nil, fmt.Errorf("line %d: confirm: %w", lineNo, err)
			}
			sc.Confirms = append(sc.Confirms, c)
		case "command":
			var c Command
			if err := json.Unmarshal(line, &c); err != nil {
				return nil, fmt.Errorf("line %d: command: %w", lineNo, err)
			}
			sc.Commands = append(sc.Commands, c)
		case "request":
			var req Request
			if err := json.Unmarshal(line, &req); err != nil {
				return nil, fmt.Errorf("line %d: request: %w", lineNo, err)
			}
			// A request opens a new turn; its stream row completes it.
			sc.Turns = append(sc.Turns, Turn{Request: &req})
		case "stream":
			var s struct {
				Events []Event `json:"events"`
			}
			if err := json.Unmarshal(line, &s); err != nil {
				return nil, fmt.Errorf("line %d: stream: %w", lineNo, err)
			}
			n := len(sc.Turns)
			if n > 0 && sc.Turns[n-1].Events == nil {
				sc.Turns[n-1].Events = s.Events
			} else {
				sc.Turns = append(sc.Turns, Turn{Events: s.Events})
			}
		case "expect_fs":
			var e FSEntry
			if err := json.Unmarshal(line, &e); err != nil {
				return nil, fmt.Errorf("line %d: expect_fs: %w", lineNo, err)
			}
			sc.ExpectFS = append(sc.ExpectFS, e)
		case "expect_outcome":
			var e ExpectOutcome
			if err := json.Unmarshal(line, &e); err != nil {
				return nil, fmt.Errorf("line %d: expect_outcome: %w", lineNo, err)
			}
			sc.ExpectOutcome = &e
		case "expect_history":
			var e struct {
				Messages []ExpectMessage `json:"messages"`
			}
			if err := json.Unmarshal(line, &e); err != nil {
				return nil, fmt.Errorf("line %d: expect_history: %w", lineNo, err)
			}
			sc.ExpectHistory = e.Messages
		case "expect_usage":
			var e ExpectUsage
			if err := json.Unmarshal(line, &e); err != nil {
				return nil, fmt.Errorf("line %d: expect_usage: %w", lineNo, err)
			}
			sc.ExpectUsage = &e
		default:
			return nil, fmt.Errorf("line %d: unknown row kind %q", lineNo, probe.Kind)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if lineNo == 0 {
		return nil, errors.New("empty fixture")
	}
	// An unfinished trailing turn (request with no stream) is an authoring
	// error.
	if n := len(sc.Turns); n > 0 && sc.Turns[n-1].Events == nil {
		return nil, errors.New("trailing request row has no stream row")
	}
	return sc, nil
}
