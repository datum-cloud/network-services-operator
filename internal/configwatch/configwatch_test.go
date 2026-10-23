package configwatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const testInterval = 5 * time.Millisecond

var configs = map[string]string{
	"a: 1\n":        "1",
	"a: 1 # note\n": "1",
	"a: 2\n":        "2",
}

func changedFrom(running string) func([]byte) (bool, error) {
	return func(data []byte) (bool, error) {
		value, ok := configs[string(data)]
		if !ok {
			return false, fmt.Errorf("invalid config %q", data)
		}
		return value != configs[running], nil
	}
}

func startWatcher(t *testing.T, path, running string) (context.CancelFunc, <-chan error) {
	t.Helper()
	rejected.Set(0)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &Watcher{
		Path:     path,
		Loaded:   []byte(running),
		Changed:  changedFrom(running),
		Interval: testInterval,
		Log:      logr.Discard(),
	}
	done := make(chan error, 1)
	go func() { done <- w.Start(ctx) }()
	return cancel, done
}

func expectRunning(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("watcher stopped early: %v", err)
	case <-time.After(20 * testInterval):
	}
}

func expectResult(t *testing.T, done <-chan error, want error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("got %v, want %v", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop")
	}
}

func expectRejected(t *testing.T, want float64) {
	t.Helper()
	if got := testutil.ToFloat64(rejected); got != want {
		t.Fatalf("nso_server_config_rejected = %v, want %v", got, want)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func TestUnchangedConfigKeepsRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")

	cancel, done := startWatcher(t, path, "a: 1\n")
	expectRunning(t, done)
	cancel()
	expectResult(t, done, nil)
}

func TestChangedConfigStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")

	_, done := startWatcher(t, path, "a: 1\n")
	expectRunning(t, done)
	writeFile(t, path, "a: 2\n")
	expectResult(t, done, ErrChanged)
}

func TestChangeBeforeWatchStartsStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")

	_, done := startWatcher(t, path, "a: 2\n")
	expectResult(t, done, ErrChanged)
}

func TestRewriteWithoutEffectKeepsRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")

	_, done := startWatcher(t, path, "a: 1\n")
	writeFile(t, path, "a: 1 # note\n")
	expectRunning(t, done)
	writeFile(t, path, "a: 2\n")
	expectResult(t, done, ErrChanged)
}

func TestInvalidChangeKeepsRunningUntilFixed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")

	_, done := startWatcher(t, path, "a: 1\n")
	writeFile(t, path, "bad\n")
	expectRunning(t, done)
	expectRejected(t, 1)
	writeFile(t, path, "a: 2\n")
	expectResult(t, done, ErrChanged)
	expectRejected(t, 0)
}

func TestInvalidChangeRevertedClearsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")

	_, done := startWatcher(t, path, "a: 1\n")
	writeFile(t, path, "bad\n")
	expectRunning(t, done)
	expectRejected(t, 1)
	writeFile(t, path, "a: 1\n")
	expectRunning(t, done)
	expectRejected(t, 0)
}

func TestUnreadableConfigKeepsRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	_, done := startWatcher(t, path, "a: 1\n")
	expectRunning(t, done)
	expectRejected(t, 1)
	writeFile(t, path, "a: 1\n")
	expectRunning(t, done)
	expectRejected(t, 0)
	writeFile(t, path, "a: 2\n")
	expectResult(t, done, ErrChanged)
}

func TestConfigMapVolumeSwapStops(t *testing.T) {
	dir := t.TempDir()
	version := func(name, content string) {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, name, "config.yaml"), content)
	}
	version("..v1", "a: 1\n")
	if err := os.Symlink("..v1", filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(filepath.Join("..data", "config.yaml"), path); err != nil {
		t.Fatal(err)
	}

	_, done := startWatcher(t, path, "a: 1\n")
	expectRunning(t, done)

	version("..v2", "a: 2\n")
	if err := os.Symlink("..v2", filepath.Join(dir, "..data_tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	expectResult(t, done, ErrChanged)
}
