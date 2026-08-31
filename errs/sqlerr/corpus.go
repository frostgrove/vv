package sqlerr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Corpus struct {
	Engine string `json:"engine"`

	Server string `json:"server"`
	Driver string `json:"driver"`
	Cases  []Case `json:"cases"`
}

const (
	KindIntegrity = "integrity"

	KindData = "data"

	KindRetryable = "retryable"

	KindInternal = "internal"

	KindNone = "none"
)

type Case struct {
	Name string `json:"name"`
	Kind string `json:"kind"`

	Want string `json:"want"`

	Stmt string `json:"stmt"`

	Unreachable string `json:"unreachable,omitempty"`
	Err         *Err   `json:"err"`
}

type Err struct {
	Type string `json:"type"`

	SQLState string `json:"sqlstate"`

	Native uint64 `json:"native"`

	Message string `json:"message"`

	Fields map[string]string `json:"fields,omitempty"`
}

func (this *Err) SameKey(o *Err) bool {
	if this == nil || o == nil {
		return this == o
	}
	if this.Type != o.Type || this.SQLState != o.SQLState || this.Native != o.Native {
		return false
	}
	return strings.Join(fieldNames(this.Fields), ",") == strings.Join(fieldNames(o.Fields), ",")
}

func (this *Err) Key() string {
	if this == nil {
		return "<no error>"
	}
	return fmt.Sprintf("%s sqlstate=%q native=%d fields=[%s]",
		this.Type, this.SQLState, this.Native, strings.Join(fieldNames(this.Fields), " "))
}

func fieldNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (this *Corpus) Case(name string) (Case, bool) {
	for _, cs := range this.Cases {
		if cs.Name == name {
			return cs, true
		}
	}
	return Case{}, false
}

func Path(dir, engine string) string { return filepath.Join(dir, engine+".json") }

func Load(dir, engine string) (*Corpus, error) {
	b, err := os.ReadFile(Path(dir, engine))
	if err != nil {
		return nil, err
	}
	var c Corpus
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(dir, engine), err)
	}
	if c.Engine != engine {
		return nil, fmt.Errorf("%s: says it is %q", Path(dir, engine), c.Engine)
	}
	return &c, nil
}

func Save(dir string, c *Corpus) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(dir, c.Engine), append(b, '\n'), 0o644)
}
