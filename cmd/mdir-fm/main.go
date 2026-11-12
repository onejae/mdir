package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gdamore/tcell/v2"
	"github.com/onejae/mdir/internal/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mdir-fm:", err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.Bool("version", false, "print version")
	config := flag.String("config", "", "configuration JSON (default: XDG config directory)")
	lastDir := flag.String("last-dir", "", "write the final directory to this file for shell integration")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "mdir-fm — classic DOS colors, Vim navigation\nUsage: mdir-fm [options] [directory]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *version {
		fmt.Println("mdir-fm 0.1.0")
		return nil
	}
	if flag.NArg() > 1 {
		return fmt.Errorf("expected at most one directory")
	}
	dir := "."
	if flag.NArg() == 1 {
		dir = flag.Arg(0)
	}
	cfg, err := app.LoadConfig(*config)
	if err != nil {
		return err
	}
	state, err := app.StatePath()
	if err != nil {
		return err
	}
	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	a, err := app.New(screen, dir, cfg, state)
	if err != nil {
		return err
	}
	if err := screen.Init(); err != nil {
		return fmt.Errorf("terminal initialization: %w", err)
	}
	defer screen.Fini()
	screen.EnableMouse(tcell.MouseButtonEvents)
	// A Ctrl-C aimed at a child shell/editor should not kill the file manager.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		return err
	}
	if *lastDir != "" {
		return os.WriteFile(*lastDir, []byte(a.Cwd+"\n"), 0600)
	}
	return nil
}
