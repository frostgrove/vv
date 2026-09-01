package jobspgfx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/frostgrove/vv/jobs"
)

var currentExecutableBuild = sync.OnceValues(resolveExecutableBuild)

func resolveExecutableBuild() (jobs.BuildID, error) {
	path, err := os.Executable()
	if err != nil {
		return jobs.BuildID{}, fmt.Errorf("resolve current executable: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return jobs.BuildID{}, fmt.Errorf("open current executable: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return jobs.BuildID{}, fmt.Errorf("hash current executable: %w", err)
	}
	build, err := jobs.ParseBuildID("exe:sha256:" + hex.EncodeToString(digest.Sum(nil)))
	if err != nil {
		return jobs.BuildID{}, fmt.Errorf("encode current executable build: %w", err)
	}
	return build, nil
}
