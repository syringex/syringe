// Command fakeprovider is a test-only stand-in for a real provider binary.
// Its behavior is driven entirely by the path argument so execprovider's
// tests can exercise success, every error kind, slow/cancelled calls, and
// malformed output — all without any real provider or cloud dependency.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/syringex/syringe/pkg/providerproto"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "init" {
		runInit()
		return
	}
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: fakeprovider <resolve|validate> <tag> <path>")
		os.Exit(1)
	}
	subcommand, path := os.Args[1], os.Args[3] // os.Args[2] (tag) is unused by this stand-in

	switch path {
	case "not-found":
		writeError(providerproto.KindNotFound, "no such secret")
	case "access-denied":
		writeError(providerproto.KindAccessDenied, "not authorized")
	case "transient":
		writeError(providerproto.KindTransient, "timed out")
	case "invalid-ref":
		writeError(providerproto.KindInvalidRef, "bad path")
	case "malformed":
		fmt.Print("not json")
	case "malformed-error":
		fmt.Print("not json")
		os.Exit(1)
	case "slow":
		time.Sleep(2 * time.Second)
		writeSuccess(subcommand, path)
	default:
		writeSuccess(subcommand, path)
	}
}

func writeSuccess(subcommand, path string) {
	enc := json.NewEncoder(os.Stdout)
	if subcommand == "validate" {
		_ = enc.Encode(providerproto.ValidateResult{OK: true})
		return
	}
	_ = enc.Encode(providerproto.ResolveResult{Value: "resolved-" + path})
}

func writeError(kind providerproto.Kind, msg string) {
	_ = json.NewEncoder(os.Stdout).Encode(providerproto.ErrorEnvelope{
		Error: providerproto.ErrorPayload{Kind: kind, Message: msg},
	})
	os.Exit(1)
}

// runInit's behavior is driven by FAKE_INIT_MODE, so execprovider's tests
// can exercise Init's success, prompting, and failure paths without a real
// provider or cloud dependency.
func runInit() {
	switch os.Getenv("FAKE_INIT_MODE") {
	case "prompt":
		fmt.Fprint(os.Stderr, "Enter value: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		_ = json.NewEncoder(os.Stdout).Encode(providerproto.InitResult{
			Env: map[string]string{"FAKE_VALUE": strings.TrimSpace(line)},
		})
	case "fail":
		writeError(providerproto.KindInvalidRef, "fake init failure")
	default:
		_ = json.NewEncoder(os.Stdout).Encode(providerproto.InitResult{Env: map[string]string{}})
	}
}
