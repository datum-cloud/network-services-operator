package configwatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

const testInterval = 5 * time.Millisecond

func startWatcher(t *testing.T, path string, loaded []byte, validate func([]byte) error) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &Watcher{Path: path, Loaded: loaded, Validate: validate, Interval: testInterval, Log: logr.Discard()}
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUnchangedConfigKeepsRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")

	cancel, done := startWatcher(t, path, []byte("a: 1\n"), nil)
	expectRunning(t, done)
	cancel()
	expectResult(t, done, nil)
}

func TestChangedConfigStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")

	_, done := startWatcher(t, path, []byte("a: 1\n"), nil)
	expectRunning(t, done)
	writeFile(t, path, "a: 2\n")
	expectResult(t, done, ErrChanged)
}

func TestChangeBeforeWatchStartsStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 2\n")

	_, done := startWatcher(t, path, []byte("a: 1\n"), nil)
	expectResult(t, done, ErrChanged)
}

func TestInvalidChangeKeepsRunningUntilFixed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, "a: 1\n")
	validate := func(data []byte) error {
		if string(data) == "bad\n" {
			return errors.New("invalid")
		}
		return nil
	}

	_, done := startWatcher(t, path, []byte("a: 1\n"), validate)
	writeFile(t, path, "bad\n")
	expectRunning(t, done)
	writeFile(t, path, "a: 2\n")
	expectResult(t, done, ErrChanged)
}

func TestUnreadableConfigKeepsRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	_, done := startWatcher(t, path, []byte("a: 1\n"), nil)
	expectRunning(t, done)
	writeFile(t, path, "a: 2\n")
	expectResult(t, done, ErrChanged)
}

func TestConfigMapVolumeSwapStops(t *testing.T) {
	dir := t.TempDir()
	mustMkdir := func(name, content string) {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, name, "config.yaml"), content)
	}
	mustMkdir("..v1", "a: 1\n")
	if err := os.Symlink("..v1", filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(filepath.Join("..data", "config.yaml"), path); err != nil {
		t.Fatal(err)
	}

	_, done := startWatcher(t, path, []byte("a: 1\n"), nil)
	expectRunning(t, done)

	mustMkdir("..v2", "a: 2\n")
	if err := os.Symlink("..v2", filepath.Join(dir, "..data_tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	expectResult(t, done, ErrChanged)
}
