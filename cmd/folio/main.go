package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"folio"
	"folio/internal/adapters"
	"folio/internal/core"
	"folio/internal/i18n"
	"folio/internal/server"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	locale, args, parseErr := adapters.ParseGlobalLanguage(os.Args[1:])
	if parseErr != nil {
		_ = adapters.WriteCLIError(os.Stdout, parseErr)
		return parseErr
	}
	ctx = adapters.WithLanguage(ctx, locale)
	if len(args) == 0 {
		return adapters.RunCLI(ctx, []string{"help"}, os.Stdin, os.Stdout, os.Stderr)
	}
	switch args[0] {
	case "version":
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"version": core.Version})
	case "init":
		f := commandFlags("init", locale)
		dir := f.String("data", "./data", i18n.Message(locale, "Private data directory"))
		demo := f.Bool("demo", false, i18n.Message(locale, "Seed fictional example posts"))
		if e := f.Parse(args[1:]); e != nil {
			if errors.Is(e, flag.ErrHelp) {
				return nil
			}
			return flagError(locale, e)
		}
		if _, e := os.Stat(filepath.Join(*dir, "token")); e == nil {
			return errors.New(i18n.Message(locale, "already initialized; existing credentials were not changed"))
		}
		if e := os.MkdirAll(*dir, 0700); e != nil {
			return e
		}
		b := make([]byte, 32)
		if _, e := rand.Read(b); e != nil {
			return e
		}
		token := hex.EncodeToString(b)
		if e := os.WriteFile(filepath.Join(*dir, "token"), []byte(token+"\n"), 0600); e != nil {
			return e
		}
		s, e := core.Open(*dir)
		if e != nil {
			return e
		}
		defer s.Close()
		if *demo {
			if e = s.Seed(ctx); e != nil {
				return e
			}
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"initialized": true, "data": *dir, "token_file": filepath.Join(*dir, "token"), "demo": *demo, "next": "folio serve --data " + *dir})
	case "serve":
		f := commandFlags("serve", locale)
		dir := f.String("data", "./data", i18n.Message(locale, "Private data directory"))
		addr := f.String("addr", "127.0.0.1:8080", i18n.Message(locale, "Listen address (loopback by default)"))
		pauseSchedules := f.Bool("pause-schedules", false, i18n.Message(locale, "Pause automatic scheduled publication for recovery review"))
		if e := f.Parse(args[1:]); e != nil {
			if errors.Is(e, flag.ErrHelp) {
				return nil
			}
			return flagError(locale, e)
		}
		token := os.Getenv("FOLIO_TOKEN")
		if token == "" {
			b, e := os.ReadFile(filepath.Join(*dir, "token"))
			if e != nil {
				return errors.New(i18n.Message(locale, "not initialized: run folio init first"))
			}
			token = strings.TrimSpace(string(b))
		}
		if len(token) < 32 {
			return errors.New(i18n.Message(locale, "FOLIO_TOKEN must contain at least 32 characters"))
		}
		s, e := core.Open(*dir)
		if e != nil {
			return e
		}
		defer s.Close()
		srv := &server.Server{Store: s, Assets: folio.Assets(), Token: token, DraftToken: os.Getenv("FOLIO_DRAFT_TOKEN"), ReadToken: os.Getenv("FOLIO_READ_TOKEN"), ProposalToken: os.Getenv("FOLIO_PROPOSAL_TOKEN"), SchedulerPaused: *pauseSchedules}
		if srv.DraftToken != "" && srv.DraftToken == srv.ReadToken {
			return errors.New(i18n.Message(locale, "draft and read tokens must be distinct"))
		}
		seenTokens := map[string]bool{token: true}
		for _, t := range []string{srv.DraftToken, srv.ReadToken, srv.ProposalToken} {
			if t != "" && (len(t) < 32 || seenTokens[t]) {
				return errors.New(i18n.Message(locale, "role tokens must be distinct and at least 32 characters"))
			}
			if t != "" {
				seenTokens[t] = true
			}
		}
		ln, e := net.Listen("tcp", *addr)
		if e != nil {
			return e
		}
		httpServer := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
		fmt.Fprint(os.Stderr, i18n.Format(locale, "FOLIO %s listening at http://%s (token values are never logged)\n", core.Version, ln.Addr()))
		schedulerDone := make(chan struct{})
		if *pauseSchedules {
			close(schedulerDone)
		} else {
			go runScheduler(ctx, s, locale, schedulerDone)
		}
		shutdownDone := make(chan struct{})
		go func() {
			<-ctx.Done()
			c, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if err := httpServer.Shutdown(c); err != nil {
				_ = httpServer.Close()
			}
			close(shutdownDone)
		}()
		e = httpServer.Serve(ln)
		if errors.Is(e, http.ErrServerClosed) {
			<-shutdownDone
			<-schedulerDone
			return nil
		}
		return e
	case "mcp":
		return adapters.RunMCP(ctx)
	default:
		return adapters.RunCLI(ctx, args, os.Stdin, os.Stdout, os.Stderr)
	}
}

func commandFlags(command, locale string) *flag.FlagSet {
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.Usage = func() {
		fmt.Fprint(os.Stderr, i18n.Format(locale, "Usage: folio %s [options]\n", command))
		f.VisitAll(func(v *flag.Flag) {
			fmt.Fprintf(os.Stderr, "  --%s\n      %s", v.Name, v.Usage)
			if v.DefValue != "" {
				fmt.Fprint(os.Stderr, i18n.Format(locale, " (default: %s)", v.DefValue))
			}
			fmt.Fprintln(os.Stderr)
		})
	}
	return f
}
func flagError(locale string, err error) error {
	m := err.Error()
	if strings.HasPrefix(m, "flag provided but not defined: ") {
		return errors.New(i18n.Format(locale, "Unknown option: %s", strings.TrimPrefix(m, "flag provided but not defined: ")))
	}
	if strings.HasPrefix(m, "flag needs an argument: ") {
		return errors.New(i18n.Format(locale, "Option requires a value: %s", strings.TrimPrefix(m, "flag needs an argument: ")))
	}
	return errors.New(i18n.Format(locale, "Invalid option value: %s", m))
}
func runScheduler(ctx context.Context, s *core.Store, locale string, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := s.RunDue(ctx, time.Now()); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, i18n.Message(locale, "Scheduled publishing check failed; it will retry."))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
