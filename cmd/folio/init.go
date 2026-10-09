package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"folio/internal/core"
	"folio/internal/i18n"
)

// Reserve the credential exclusively before initializing storage. A failed
// attempt removes only its own credential so the same directory can be retried;
// existing data is never removed or an existing credential overwritten.
func initialize(ctx context.Context, dir string, demo bool, locale string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tokenPath := filepath.Join(dir, "token")
	f, err := os.OpenFile(tokenPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return errors.New(i18n.Message(locale, "already initialized; existing credentials were not changed"))
	}
	if err != nil {
		return err
	}
	defer f.Close()
	created, err := f.Stat()
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = f.Close()
			if current, err := os.Lstat(tokenPath); err == nil && os.SameFile(created, current) {
				_ = os.Remove(tokenPath)
			}
		}
	}()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	if _, err := f.WriteString(hex.EncodeToString(b) + "\n"); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	s, err := core.Open(dir)
	if err != nil {
		return err
	}
	if demo {
		if err := s.Seed(ctx); err != nil {
			_ = s.Close()
			return err
		}
	}
	if err := s.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}
