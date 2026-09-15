// Command inject-provider-gcp is a standalone provider binary for GCP
// Secret Manager (gcp-sm). It speaks the protocol defined in
// pkg/providerproto and is invoked by inject as a subprocess — its GCP SDK
// dependency never touches the core inject binary.
//
// Usage: inject-provider-gcp <resolve|validate> <tag> <path>
//
//	inject-provider-gcp init
//
// tag is always "gcp-sm" here (this provider serves exactly one tag,
// unlike aws's shared aws-sm/aws-ssm identity) — inject still passes it
// explicitly per the protocol, and it's rejected if it's anything else.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
	"golang.org/x/oauth2/google"
	"golang.org/x/term"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/syringex/syringe/pkg/providerproto"
)

const tagSecretManager = "gcp-sm"

// version is this provider's own version (independent of inject core's),
// set at build time via -ldflags -X from providers/manifest.json's
// "version" field for the "gcp" entry (see internal/cmd/init.go and the CI
// workflows). It's embedded purely for forensic/debugging purposes —
// syringe.lock is what actually records which version was installed,
// verified independently via a digest.
var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "init" {
		env, errPayload := runInit(context.Background(), os.Stdin, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())))
		if errPayload != nil {
			fail(errPayload.Kind, errPayload.Message)
		}
		succeed(providerproto.InitResult{Env: env})
		return
	}
	if len(os.Args) != 4 {
		fail(providerproto.KindInvalidRef, "usage: inject-provider-gcp <resolve|validate> <tag> <path>")
	}
	subcommand, tag, path := os.Args[1], os.Args[2], os.Args[3]
	if tag != tagSecretManager {
		fail(providerproto.KindInvalidRef, "unknown tag "+tag+" for the gcp provider (want gcp-sm)")
	}

	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		fail(providerproto.KindTransient, "creating Secret Manager client: "+err.Error())
	}
	defer client.Close()

	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		fail(providerproto.KindInvalidRef, "GOOGLE_CLOUD_PROJECT is not set — run `inject init`")
	}

	switch subcommand {
	case "resolve":
		res, errPayload := resolveSecret(ctx, client, project, path)
		if errPayload != nil {
			fail(errPayload.Kind, errPayload.Message)
		}
		succeed(res)
	case "validate":
		if errPayload := validateSecret(ctx, client, project, path); errPayload != nil {
			fail(errPayload.Kind, errPayload.Message)
		}
		succeed(providerproto.ValidateResult{OK: true})
	default:
		fail(providerproto.KindInvalidRef, "unknown subcommand "+subcommand)
	}
}

// runInit always asks which GCP project this project should use — it
// deliberately never silently inherits whatever the ambient gcloud/ADC
// config happens to have, so a project's choice stays explicit and stable
// even if the machine's own GCP config changes later. It never asks for
// credentials: Secret Manager always authenticates via GCP's Application
// Default Credentials (gcloud's own ADC, a service account key file via
// GOOGLE_APPLICATION_CREDENTIALS, GCE/GKE metadata, workload identity,
// ...) — this provider never stores, asks for, or otherwise handles raw
// credentials itself. It refuses to block forever when stdin isn't an
// interactive terminal — there's deliberately no env-var bypass for that
// case; an unattended install must hand-edit config.toml instead.
//
// in/errOut and isTerminal are injected so this is fully unit-testable
// without a real terminal or subprocess.
func runInit(ctx context.Context, in io.Reader, errOut io.Writer, isTerminal bool) (map[string]string, *providerproto.ErrorPayload) {
	if !isTerminal {
		return nil, &providerproto.ErrorPayload{
			Kind:    providerproto.KindInvalidRef,
			Message: "GCP project setup requires an interactive terminal — run `inject init` from a terminal, or hand-write .syringe/providers/gcp/config.toml",
		}
	}

	reader := bufio.NewReader(in)

	// Shown only as a hint — never applied silently.
	currentProject := resolveDefaultProject(ctx)
	prompt := "GCP project to use"
	if currentProject != "" {
		prompt += fmt.Sprintf(" [%s]", currentProject)
	}
	fmt.Fprint(errOut, prompt+": ")
	project, err := chooseProject(readLine(reader), currentProject)
	if err != nil {
		return nil, &providerproto.ErrorPayload{Kind: providerproto.KindInvalidRef, Message: err.Error()}
	}

	return map[string]string{"GOOGLE_CLOUD_PROJECT": project}, nil
}

// readLine reads one line and trims its trailing newline/whitespace.
func readLine(r *bufio.Reader) string {
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

// resolveDefaultProject looks up the project ID from Application Default
// Credentials (e.g. what `gcloud auth application-default login` stored,
// or a service account key's own project), purely to show as a hint. Any
// error, or a credential source with no project ID, just means no hint is
// shown.
func resolveDefaultProject(ctx context.Context) string {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return ""
	}
	return creds.ProjectID
}

// chooseProject is a pure function so the "typed value wins, otherwise
// fall back to the hinted default, otherwise it's an error" logic is
// unit-testable without any I/O or GCP call.
func chooseProject(input, current string) (string, error) {
	if input != "" {
		return input, nil
	}
	if current != "" {
		return current, nil
	}
	return "", errors.New("a GCP project is required")
}

// secretManagerClient is the subset of *secretmanager.Client that
// resolveSecret and validateSecret use, narrowed to an interface purely so
// tests can supply a fake instead of talking to real GCP.
type secretManagerClient interface {
	AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
	GetSecretVersion(ctx context.Context, req *secretmanagerpb.GetSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.SecretVersion, error)
}

// resolveSecret returns either a result or a classified error payload —
// never both — so it stays a plain, testable function; only main() decides
// how to turn that into process exit behavior.
//
// ref is the secret's short ID (not GCP's full "projects/.../secrets/..."
// resource name — project comes from GOOGLE_CLOUD_PROJECT, and the version
// is always "latest"; there's no version-pinning syntax yet, unlike AWS
// SSM's native support for one), optionally followed by "#<key>" to select
// one field out of a secret stored as a JSON object — "#" is never a legal
// character in a GCP secret ID, so the split is unambiguous.
func resolveSecret(ctx context.Context, client secretManagerClient, project, ref string) (providerproto.ResolveResult, *providerproto.ErrorPayload) {
	secretID, jsonKey := splitJSONKey(ref)

	resp, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: versionName(project, secretID),
	})
	if err != nil {
		return providerproto.ResolveResult{}, &providerproto.ErrorPayload{Kind: classify(err), Message: formatGCPError(err)}
	}
	if resp.Payload == nil {
		return providerproto.ResolveResult{}, &providerproto.ErrorPayload{Kind: providerproto.KindNotFound, Message: "secret has no payload"}
	}
	data := resp.Payload.Data

	if jsonKey != "" {
		if !utf8.Valid(data) {
			return providerproto.ResolveResult{}, &providerproto.ErrorPayload{Kind: providerproto.KindInvalidRef, Message: "cannot select a key (#" + jsonKey + ") from a non-text secret"}
		}
		value, errPayload := extractJSONKey(string(data), jsonKey)
		if errPayload != nil {
			return providerproto.ResolveResult{}, errPayload
		}
		return providerproto.ResolveResult{Value: value}, nil
	}

	// .env values are text. GCP secrets are just bytes with no separate
	// string/binary type the way AWS Secrets Manager has, so a secret that
	// isn't valid UTF-8 is surfaced base64-encoded rather than mangled.
	if utf8.Valid(data) {
		return providerproto.ResolveResult{Value: string(data)}, nil
	}
	return providerproto.ResolveResult{Value: base64.StdEncoding.EncodeToString(data)}, nil
}

// validateSecret uses GetSecretVersion, which never returns the secret's
// payload, as the cheaper existence/permission check the Provider.Validate
// contract calls for. A nil return means success. It only confirms the
// secret version itself exists/is accessible — a "#key" suffix is ignored,
// since checking a specific key would require fetching and parsing the
// value, defeating the point of a cheap, value-free check.
func validateSecret(ctx context.Context, client secretManagerClient, project, ref string) *providerproto.ErrorPayload {
	secretID, _ := splitJSONKey(ref)
	_, err := client.GetSecretVersion(ctx, &secretmanagerpb.GetSecretVersionRequest{
		Name: versionName(project, secretID),
	})
	if err != nil {
		return &providerproto.ErrorPayload{Kind: classify(err), Message: formatGCPError(err)}
	}
	return nil
}

// versionName builds the fully-qualified GCP resource name for a secret's
// latest version.
func versionName(project, secretID string) string {
	return fmt.Sprintf("projects/%s/secrets/%s/versions/latest", project, secretID)
}

// splitJSONKey splits ref into a secret ID and an optional JSON key
// selector after the first "#". No selector returns key == "".
func splitJSONKey(ref string) (id, key string) {
	if before, after, found := strings.Cut(ref, "#"); found {
		return before, after
	}
	return ref, ""
}

// extractJSONKey parses raw as a JSON object and returns the value at key
// as a string. A non-string JSON value (number, bool, nested object/array)
// is re-encoded to its JSON representation so the result is always a
// deterministic, usable text value.
func extractJSONKey(raw, key string) (string, *providerproto.ErrorPayload) {
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return "", &providerproto.ErrorPayload{Kind: providerproto.KindInvalidRef, Message: fmt.Sprintf("value is not a JSON object, cannot select key %q", key)}
	}
	v, ok := fields[key]
	if !ok || v == nil {
		// A JSON null is treated the same as a missing key: rendering it as
		// the literal string "null" would silently satisfy a required
		// secret with a bogus four-character value instead of a clear error.
		return "", &providerproto.ErrorPayload{Kind: providerproto.KindNotFound, Message: fmt.Sprintf("key %q not found in secret", key)}
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", &providerproto.ErrorPayload{Kind: providerproto.KindTransient, Message: "encoding selected key: " + err.Error()}
	}
	return string(b), nil
}

// formatGCPError renders a clean "<code>: <message>" string for a GCP API
// error by extracting just its gRPC status code/message, instead of the
// SDK's often deeply-nested transport wrapper chain. An error that isn't a
// gRPC status (e.g. a local network failure) falls back to its own
// message, which is already reasonably clear.
func formatGCPError(err error) string {
	st, ok := status.FromError(err)
	if !ok || st.Code() == codes.Unknown {
		return err.Error()
	}
	return st.Code().String() + ": " + st.Message()
}

// classify maps a GCP API error to a protocol Kind via its gRPC status
// code. Anything not explicitly recognized (Unavailable, DeadlineExceeded,
// Internal, ResourceExhausted/throttling, a non-gRPC error, ...) falls
// back to transient, which is the safe default for "retry later" behavior.
func classify(err error) providerproto.Kind {
	st, ok := status.FromError(err)
	if !ok {
		return providerproto.KindTransient
	}
	switch st.Code() {
	case codes.NotFound:
		return providerproto.KindNotFound
	case codes.PermissionDenied, codes.Unauthenticated:
		return providerproto.KindAccessDenied
	case codes.InvalidArgument, codes.FailedPrecondition:
		return providerproto.KindInvalidRef
	}
	return providerproto.KindTransient
}

func succeed(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func fail(kind providerproto.Kind, message string) {
	_ = json.NewEncoder(os.Stdout).Encode(providerproto.ErrorEnvelope{
		Error: providerproto.ErrorPayload{Kind: kind, Message: message},
	})
	os.Exit(1)
}
