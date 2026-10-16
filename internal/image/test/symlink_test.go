package test

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"kh/internal/image"
)

// A subprocess bounds the regression even if Open accidentally becomes blocking
// again: a regular-file symlink can turn into a FIFO between Stat and Open.
func TestReadImageSymlinkRace(t *testing.T) {
	if os.Getenv("KH_IMAGE_SYMLINK_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReadImageSymlinkRace$", "-test.count=1")
		cmd.Env = append(os.Environ(), "KH_IMAGE_SYMLINK_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("symlink image read blocked or failed: %s %v (context=%v)", out, err, ctx.Err())
		}
		return
	}
	dir := t.TempDir()
	file, fifo, alias, staged := filepath.Join(dir, "image.png"), filepath.Join(dir, "pipe"), filepath.Join(dir, "image-link"), filepath.Join(dir, "next-link")
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, alias); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	failures := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			target := file
			if i%2 == 0 {
				target = fifo
			}
			if err := os.Symlink(target, staged); err != nil {
				failures <- err
				return
			}
			if err := os.Rename(staged, alias); err != nil {
				failures <- err
				return
			}
			runtime.Gosched()
		}
	}()
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 48; j++ {
				_, _ = image.Read(alias)
			}
		}()
	}
	close(start)
	wg.Wait()
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
}
