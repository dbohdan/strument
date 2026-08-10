package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CostEntry is one turn's accounting, as one line of cost.jsonl.
//
// The same numbers the closing usage line prints, kept as data rather than as
// prose. The transcript already says what a turn cost in a sentence; this
// answers "what has this project cost me" and "which model is actually worth
// it" without re-reading a year of markdown. A prompt A/B run in August put the
// question sharply: the sample size ended up set by the most expensive model
// rather than by the question, and nothing on disk could have said so.
type CostEntry struct {
	Time         string   `json:"time"`
	Model        string   `json:"model"`
	TokensSent   int      `json:"tokens_sent"`
	TokensRecv   int      `json:"tokens_received"`
	CacheRead    int      `json:"cache_read,omitempty"`
	CacheWrite   int      `json:"cache_write,omitempty"`
	Cost         *float64 `json:"cost,omitempty"` // absent when the provider reported none
	Estimated    bool     `json:"estimated,omitempty"`
	Steps        int      `json:"steps"`
	FilesChanged int      `json:"files_changed,omitempty"`
}

// CostPath is the ledger for a project root.
func CostPath(projectRoot string) (string, error) {
	dir, err := ProjectDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cost.jsonl"), nil
}

// AppendCost adds one turn to the ledger.
//
// JSON Lines, appended, never rewritten: one line is one turn, a partial write
// costs at most the last line, and `cat projects/*/cost.jsonl` aggregates across
// every project — which is the query the per-project layout would otherwise make
// harder. At roughly a hundred bytes a turn there is no pruning policy to get
// wrong; a year of heavy use is under a megabyte.
func AppendCost(projectRoot string, e CostEntry) error {
	p, err := CostPath(projectRoot)
	if err != nil {
		return err
	}
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}
