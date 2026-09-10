package jobsfx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/frostgrove/vv/jobs"
)

type panicNilBackendPreparer struct{}

func (panicNilBackendPreparer) Prepare(context.Context) error { panic(nil) }

func TestPrepareBackendPanicNilFailsClosed(t *testing.T) {
	if os.Getenv("VV_JOBSFX_PANICNIL_HELPER") == "1" {
		if err := prepareBackend(context.Background(), panicNilBackendPreparer{}); !errors.Is(err, jobs.ErrDriver) {
			t.Fatalf("panic(nil) backend preparation = %v", err)
		}
		return
	}
	environment := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GODEBUG=") {
			environment = append(environment, value)
		}
	}
	command := exec.Command(os.Args[0], "-test.run=^TestPrepareBackendPanicNilFailsClosed$")
	command.Env = append(environment, "GODEBUG=panicnil=1", "VV_JOBSFX_PANICNIL_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("panic(nil) subprocess: %v\n%s", err, output)
	}
}
