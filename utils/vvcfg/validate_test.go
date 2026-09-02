package vvcfg

import (
	"errors"
	"strings"
	"testing"
)

type listenBlock struct {
	Addr string `yaml:"addr"`
}

func (this *listenBlock) ValidateSelf() error {
	if this.Addr == "" {
		return errors.New("addr is required")
	}
	return nil
}

type storeBlock struct {
	DSN string `yaml:"dsn"`
}

func (this *storeBlock) ValidateSelf() error {
	if this.DSN == "" {
		return errors.New("dsn is required")
	}
	return nil
}

type serviceConf struct {
	Listen listenBlock `yaml:"listen"`
	Store  storeBlock  `yaml:"store"`
}

type countingNode struct {
	Name  string        `yaml:"name"`
	Next  *countingNode `yaml:"next"`
	calls *int
}

func (this *countingNode) ValidateSelf() error {
	*this.calls++
	return nil
}

type orderedRoot struct {
	Child orderedChild `yaml:"child"`
	trace *[]string
}

func (this *orderedRoot) ValidateSelf() error {
	*this.trace = append(*this.trace, "root self")
	return nil
}

func (this *orderedRoot) ValidateCross() error {
	*this.trace = append(*this.trace, "root cross")
	return nil
}

type orderedChild struct {
	Broken bool `yaml:"broken"`
	trace  *[]string
}

func (this *orderedChild) ValidateSelf() error {
	*this.trace = append(*this.trace, "child self")
	if this.Broken {
		return errors.New("the child is broken")
	}
	return nil
}

func TestEveryBlockThatCanRefuseItselfIsAskedWithoutAForwarder(t *testing.T) {
	err := ValidateTree(&serviceConf{})
	if err == nil {
		t.Fatal("two empty blocks that both refuse themselves loaded clean")
	}
	if !strings.Contains(err.Error(), "listen: addr is required") {
		t.Fatalf("the failure does not name the block it came from: %v", err)
	}
	if !strings.Contains(err.Error(), "store: dsn is required") {
		t.Fatalf("the second broken block was not reported, so an operator learns one per restart: %v", err)
	}
	if err := ValidateTree(&serviceConf{Listen: listenBlock{Addr: ":8080"}, Store: storeBlock{DSN: "postgres://x"}}); err != nil {
		t.Fatalf("a tree whose blocks are all valid was refused: %v", err)
	}
}

func TestANodeReachableTwiceIsAskedToValidateItselfOnce(t *testing.T) {
	calls := 0
	first := &countingNode{Name: "first", calls: &calls}
	second := &countingNode{Name: "second", calls: &calls}
	first.Next = second
	second.Next = first

	if err := ValidateTree(first); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("ValidateSelf ran %d times over a two-node cycle, want one call per node", calls)
	}
}

func TestCrossRulesRunAfterEveryNodeHasValidatedItselfAndNotOverABrokenTree(t *testing.T) {
	var trace []string
	sound := &orderedRoot{trace: &trace}
	sound.Child.trace = &trace
	if err := ValidateTree(sound); err != nil {
		t.Fatal(err)
	}
	if strings.Join(trace, ",") != "root self,child self,root cross" {
		t.Fatalf("order = %v, want every self before any cross", trace)
	}

	trace = nil
	broken := &orderedRoot{trace: &trace}
	broken.Child.trace = &trace
	broken.Child.Broken = true
	if err := ValidateTree(broken); err == nil {
		t.Fatal("a broken child did not stop the tree")
	}
	for _, step := range trace {
		if step == "root cross" {
			t.Fatal("a cross rule ran over a tree whose nodes are not individually valid")
		}
	}
}

func TestAFileWithTwoBrokenBlocksReportsBoth(t *testing.T) {
	path := write(t, "listen:\n  addr: \"\"\nstore:\n  dsn: \"\"\n")
	_, err := Load[serviceConf](path)
	if err == nil {
		t.Fatal("a file whose nested blocks both refuse themselves loaded")
	}
	if !strings.Contains(err.Error(), "listen: addr is required") || !strings.Contains(err.Error(), "store: dsn is required") {
		t.Fatalf("Load did not walk the tree: %v", err)
	}
}

func TestAValidationFailureIsReachableAsAValueCarryingItsPath(t *testing.T) {
	err := ValidateTree(&serviceConf{Store: storeBlock{DSN: "postgres://x"}})
	var failure *ValidationError
	if !errors.As(err, &failure) {
		t.Fatalf("the failure is not a *ValidationError: %v", err)
	}
	if failure.Path != "listen" {
		t.Fatalf("path = %q, want the block the rule belongs to", failure.Path)
	}
}

type mergingBlock struct {
	Engine   string        `yaml:"engine"`
	Fragment *mergingBlock `yaml:"fragment"`
}

func (this mergingBlock) Validate() error {
	if this.Engine == "" {
		return errors.New("engine is required")
	}
	return nil
}

type fragmentedConf struct {
	Store mergingBlock `yaml:"store"`
}

type selfFragmentedConf struct {
	Store selfMergingBlock `yaml:"store"`
}

type selfMergingBlock struct {
	Engine   string            `yaml:"engine"`
	Fragment *selfMergingBlock `yaml:"fragment"`
}

func (this selfMergingBlock) ValidateSelf() error {
	if this.Engine == "" {
		return errors.New("engine is required")
	}
	return nil
}

func TestABlockSpellingTheOlderValidateOwnsWhatIsUnderIt(t *testing.T) {
	whole := &fragmentedConf{Store: mergingBlock{Engine: "postgres", Fragment: &mergingBlock{}}}
	if err := ValidateTree(whole); err != nil {
		t.Fatalf("a fragment its own block merges before checking was refused on its own: %v", err)
	}

	shaped := &selfFragmentedConf{Store: selfMergingBlock{Engine: "postgres", Fragment: &selfMergingBlock{}}}
	if err := ValidateTree(shaped); err == nil {
		t.Fatal("a block that promises ValidateSelf is about itself alone did not have its child asked")
	}
}
