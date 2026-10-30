package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	configcontract "github.com/RakuenSoftware/aimee-module-config/server-go/config"
	handler "github.com/RakuenSoftware/aimee-module-config/server-go/modules/config"
	"github.com/RakuenSoftware/ouroboros-ring/go/ring"
)

// changes keeps only the newest version not yet announced: a caller needs the
// version that is current, not every one it missed.
type changes chan string

func (c changes) offer(version string) {
	for {
		select {
		case c <- version:
			return
		default:
		}
		select {
		case <-c:
		default:
		}
	}
}

// announce publishes config-changed for every version change until ctx ends.
// A version equal to the last one announced (the module's own write, seen
// again by the file watch) is dropped.
func announce(ctx context.Context, publisher ring.Publisher, pending changes) {
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case version := <-pending:
			if version == last {
				continue
			}
			payload, err := json.Marshal(map[string]string{"version": version})
			if err != nil {
				continue
			}
			if err := publisher.Publish(ctx, configcontract.EventConfigChanged, payload); err != nil {
				if ctx.Err() == nil {
					log.Printf("aimee-module-config: announce version %s: %v", version, err)
				}
				continue
			}
			last = version
		}
	}
}

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
	path, err := handler.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "aimee-module-config: %v\n", err)
		os.Exit(1)
	}
	store, err := handler.NewStore(path)
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
	pending := make(changes, 1)
	store.OnChange(pending.offer)
	go func() {
		if err := store.Watch(ctx); err != nil && ctx.Err() == nil {
			log.Printf("aimee-module-config: outside edits will not be announced: %v", err)
		}
	}()
	config := ring.ModuleProcessConfig{
		SocketPath: os.Args[1], ModuleName: "config",
		PrincipalClass: 1, PrincipalRef: 2,
		Stages: []ring.ModuleStage{
			{EventKind: configcontract.EventConfig, StageID: configcontract.StageConfig},
		},
		Handler: handler.NewHandler(store),
		// Callers cache the snapshot and drop it when told it changed (ring
		// D14), instead of asking for the version on a timer.
		Started: func(publisher ring.Publisher) { go announce(ctx, publisher, pending) },
	}
	if err := ring.RunModuleProcess(ctx, config); err != nil {
		fmt.Fprintf(os.Stderr, "aimee-module-config: %v\n", err)
		os.Exit(1)
	}
}
