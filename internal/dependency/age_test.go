package dependency_test

import (
	"encoding/json"
	"io"
	"os/exec"
	"testing"
	"time"
)

func TestDependenciesAreAtLeastFourteenDaysOld(t *testing.T) {
	command := exec.Command("go", "list", "-m", "-json", "all")
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(output)
	cutoff := time.Now().UTC().AddDate(0, 0, -14)
	for {
		var module struct {
			Path string
			Main bool
			Time time.Time
		}
		if err := decoder.Decode(&module); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if !module.Main && (module.Time.IsZero() || module.Time.After(cutoff)) {
			t.Errorf("dependency %s is not confirmed to be at least 14 days old", module.Path)
		}
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
}
