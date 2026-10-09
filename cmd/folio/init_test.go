package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInitializeFailureCanRetryWithoutDeletingData(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "folio.db")
	original := []byte("not a SQLite database; preserve this obstruction")
	if err := os.WriteFile(database, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := initialize(context.Background(), dir, false, "en"); err == nil {
		t.Fatal("invalid database accepted")
	}
	if _, err := os.Lstat(filepath.Join(dir, "token")); !os.IsNotExist(err) {
		t.Fatalf("failed initialization left a token: %v", err)
	}
	actual, err := os.ReadFile(database)
	if err != nil || string(actual) != string(original) {
		t.Fatal("existing database changed on failure")
	}
	// The user corrects the original storage obstruction, then retries init.
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	if err := initialize(context.Background(), dir, false, "en"); err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil || len(strings.TrimSpace(string(token))) != 64 {
		t.Fatal("missing complete credential")
	}
	info, err := os.Stat(filepath.Join(dir, "token"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions are not private")
	}
	if err := initialize(context.Background(), dir, false, "en"); err == nil {
		t.Fatal("repeat initialization accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "token"))
	if string(after) != string(token) {
		t.Fatal("existing credential changed")
	}
}

func TestInitializeRefusesDanglingCredentialSymlink(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	target := filepath.Join(elsewhere, "must-not-be-created")
	if err := os.Symlink(target, filepath.Join(dir, "token")); err != nil {
		t.Fatal(err)
	}
	if err := initialize(context.Background(), dir, false, "zh-CN"); err == nil {
		t.Fatal("credential symlink accepted")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("initialization followed the credential symlink")
	}
	if _, err := os.Readlink(filepath.Join(dir, "token")); err != nil {
		t.Fatal("pre-existing symlink changed")
	}
}

func TestConcurrentInitializeHasOneCredentialOwner(t *testing.T) {
	dir := t.TempDir()
	const contenders = 16
	start, results := make(chan struct{}), make(chan error, contenders)
	var wg sync.WaitGroup
	for range contenders {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- initialize(context.Background(), dir, false, "en") }()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "already initialized") {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("%d successful initializers", successes)
	}
	token, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil || len(strings.TrimSpace(string(token))) != 64 {
		t.Fatal("winner's credential missing or incomplete")
	}
}
