package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/RakuenSoftware/aimee-module-config/server-go/bus"
	handler "github.com/RakuenSoftware/aimee-module-config/server-go/modules/config"
)

func printDefaultValue(key string, output io.Writer) error {
	path, err := handler.DefaultPath()
	if err != nil {
		return err
	}
	store, err := handler.NewStore(path)
	if err != nil {
		return err
	}
	value, found, err := store.StringValue(key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("config key %q is absent or is not a public string", key)
	}
	_, err = fmt.Fprintln(output, value)
	return err
}

func printDefaultSnapshot(output io.Writer) error {
	path, err := handler.DefaultPath()
	if err != nil {
		return err
	}
	store, err := handler.NewStore(path)
	if err != nil {
		return err
	}
	values, version, err := store.Snapshot()
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{"values": values, "version": version})
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--get" {
		if err := printDefaultValue(os.Args[2], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "aimee-module-config: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--snapshot" {
		if err := printDefaultSnapshot(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "aimee-module-config: %v\n", err)
			os.Exit(1)
		}
		return
	}
	moduleHandler, err := handler.NewDefaultHandler()
	if err != nil {
		fmt.Fprintf(os.Stderr, "aimee-module-config: %v\n", err)
		os.Exit(1)
	}
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s DAEMON_MODULE_BUS_SOCKET | --get PUBLIC_STRING_KEY | --snapshot\n", os.Args[0])
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	config := bus.ModuleProcessConfig{
		SocketPath: os.Args[1], ModuleName: "config",
		PrincipalClass: 1, PrincipalRef: 2,
		Stages: []bus.ModuleStage{
			{EventKind: 4609, StageID: 1},
		},
		Handler: moduleHandler,
	}
	if err := bus.RunModuleProcess(ctx, config); err != nil {
		fmt.Fprintf(os.Stderr, "aimee-module-config: %v\n", err)
		os.Exit(1)
	}
}
