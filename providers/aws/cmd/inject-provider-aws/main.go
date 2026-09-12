// Command inject-provider-aws is a standalone provider binary covering two
// AWS secret backends — Secrets Manager (aws-sm) and SSM Parameter Store
// (aws-ssm) — as one provider identity, since both share the same AWS
// profile/region/credentials and it doesn't make sense to install or
// configure them separately. It speaks the protocol defined in
// pkg/providerproto and is invoked by inject as a subprocess — its AWS SDK
// dependency never touches the core inject binary.
//
// Usage: inject-provider-aws <resolve|validate> <tag> <path>
//
//	inject-provider-aws init
//
// tag is which AWS service the call is for (aws-sm or aws-ssm) — inject
// passes it explicitly since a single installed provider now serves
// multiple reference tags. init has no tag: it always configures the one
// shared AWS profile/region, always interactively, and it never silently
// inherits the ambient AWS CLI config, and never asks for credentials.
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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/term"

	"github.com/syringex/syringe/pkg/providerproto"
)

const (
	tagSecretsManager = "aws-sm"
	tagParameterStore = "aws-ssm"
)

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
		fail(providerproto.KindInvalidRef, "usage: inject-provider-aws <resolve|validate> <tag> <path>")
	}
	subcommand, tag, path := os.Args[1], os.Args[2], os.Args[3]

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		fail(providerproto.KindTransient, "loading AWS config: "+err.Error())
	}

	var resolveFn func(context.Context, string) (providerproto.ResolveResult, *providerproto.ErrorPayload)
	var validateFn func(context.Context, string) *providerproto.ErrorPayload
	switch tag {
	case tagParameterStore:
		client := ssm.NewFromConfig(cfg)
		resolveFn = func(ctx context.Context, p string) (providerproto.ResolveResult, *providerproto.ErrorPayload) {
			return resolveParameter(ctx, client, p)
		}
		validateFn = func(ctx context.Context, p string) *providerproto.ErrorPayload {
			return validateParameter(ctx, client, p)
		}
	case tagSecretsManager:
		client := secretsmanager.NewFromConfig(cfg)
		resolveFn = func(ctx context.Context, p string) (providerproto.ResolveResult, *providerproto.ErrorPayload) {
			return resolveSecret(ctx, client, p)
		}
		validateFn = func(ctx context.Context, p string) *providerproto.ErrorPayload {
			return validateSecret(ctx, client, p)
		}
	default:
		fail(providerproto.KindInvalidRef, "unknown tag "+tag+" for the aws provider (want aws-sm or aws-ssm)")
	}

	switch subcommand {
	case "resolve":
		res, errPayload := resolveFn(ctx, path)
		if errPayload != nil {
			fail(errPayload.Kind, errPayload.Message)
		}
		succeed(res)
	case "validate":
		if errPayload := validateFn(ctx, path); errPayload != nil {
			fail(errPayload.Kind, errPayload.Message)
		}
		succeed(providerproto.ValidateResult{OK: true})
	default:
		fail(providerproto.KindInvalidRef, "unknown subcommand "+subcommand)
	}
}

// runInit always asks which AWS profile and region this project should use
// — it deliberately never silently inherits whatever the ambient AWS CLI
// config/environment happens to have, so a project's choice stays explicit
// and stable even if the machine's own AWS config changes later. It never
// asks for credentials: the profile is just a name pointing at credentials
// already set up via the AWS CLI/SSO/etc., not the credentials themselves.
// It refuses to block forever when stdin isn't an interactive terminal —
// there's deliberately no env-var bypass for that case, since silently
// trusting ambient env vars is exactly what this is meant to avoid; an
// unattended install must hand-edit config.toml instead.
//
// This configures the one shared AWS profile/region used by both aws-sm
// and aws-ssm — it's not per-tag.
//
// in/errOut and isTerminal are injected so this is fully unit-testable
// without a real terminal or subprocess.
func runInit(ctx context.Context, in io.Reader, errOut io.Writer, isTerminal bool) (map[string]string, *providerproto.ErrorPayload) {
	if !isTerminal {
		return nil, &providerproto.ErrorPayload{
			Kind:    providerproto.KindInvalidRef,
			Message: "AWS profile/region setup requires an interactive terminal — run `inject init` from a terminal, or hand-write .syringe/providers/aws/config.toml",
		}
	}

	reader := bufio.NewReader(in)
	env := map[string]string{}

	fmt.Fprint(errOut, "AWS profile to use (leave blank for the default credential chain): ")
	profile := readLine(reader)
	if profile != "" {
		env["AWS_PROFILE"] = profile
	}

	// Shown only as a hint for the chosen profile — never applied silently.
	currentRegion := resolveRegion(ctx, profile)
	prompt := "AWS region to use"
	if currentRegion != "" {
		prompt += fmt.Sprintf(" [%s]", currentRegion)
	}
	fmt.Fprint(errOut, prompt+": ")
	region, err := chooseRegion(readLine(reader), currentRegion)
	if err != nil {
		return nil, &providerproto.ErrorPayload{Kind: providerproto.KindInvalidRef, Message: err.Error()}
	}
	env["AWS_REGION"] = region

	return env, nil
}

// readLine reads one line and trims its trailing newline/whitespace.
func readLine(r *bufio.Reader) string {
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

// resolveRegion loads the region that would currently resolve for profile
// (blank meaning "no profile override"), purely to show as a hint. Any
// error (e.g. an unknown profile) just means no hint is shown.
func resolveRegion(ctx context.Context, profile string) string {
	var opts []func(*config.LoadOptions) error
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return ""
	}
	return cfg.Region
}

// chooseRegion is a pure function so the "typed value wins, otherwise fall
// back to the hinted default, otherwise it's an error" logic is
// unit-testable without any I/O or AWS SDK call.
func chooseRegion(input, current string) (string, error) {
	if input != "" {
		return input, nil
	}
	if current != "" {
		return current, nil
	}
	return "", errors.New("a region is required")
}

// secretsManagerClient is the subset of *secretsmanager.Client that
// resolveSecret and validateSecret use, narrowed to an interface purely so
// tests can supply a fake instead of talking to real AWS.
type secretsManagerClient interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	DescribeSecret(ctx context.Context, params *secretsmanager.DescribeSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error)
}

// resolveSecret returns either a result or a classified error payload —
// never both — so it stays a plain, testable function; only main() decides
// how to turn that into process exit behavior.
//
// ref is the secret ID, optionally followed by "#<key>" to select one field
// out of a secret stored as a JSON object (e.g. AWS RDS-managed secrets, or
// any secret holding {"username":"...","password":"..."} style key-value
// pairs) — "#" is never a legal character in an AWS secret name, so the
// split is unambiguous.
func resolveSecret(ctx context.Context, client secretsManagerClient, ref string) (providerproto.ResolveResult, *providerproto.ErrorPayload) {
	secretID, jsonKey := splitJSONKey(ref)

	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretID)})
	if err != nil {
		return providerproto.ResolveResult{}, &providerproto.ErrorPayload{Kind: classify(err), Message: formatAWSError(err)}
	}
	switch {
	case out.SecretString != nil:
		if jsonKey == "" {
			return providerproto.ResolveResult{Value: *out.SecretString}, nil
		}
		value, errPayload := extractJSONKey(*out.SecretString, jsonKey)
		if errPayload != nil {
			return providerproto.ResolveResult{}, errPayload
		}
		return providerproto.ResolveResult{Value: value}, nil
	case out.SecretBinary != nil:
		if jsonKey != "" {
			return providerproto.ResolveResult{}, &providerproto.ErrorPayload{Kind: providerproto.KindInvalidRef, Message: "cannot select a key (#" + jsonKey + ") from a binary secret"}
		}
		// .env values are text, so a binary secret is surfaced base64-encoded
		// rather than treated as unresolvable.
		return providerproto.ResolveResult{Value: base64.StdEncoding.EncodeToString(out.SecretBinary)}, nil
	default:
		return providerproto.ResolveResult{}, &providerproto.ErrorPayload{Kind: providerproto.KindNotFound, Message: "secret has neither a string nor a binary value"}
	}
}

// validateSecret uses DescribeSecret, which never returns the secret's
// value, as the cheaper existence/permission check the Provider.Validate
// contract calls for. A nil return means success. It only confirms the
// secret itself exists/is accessible — a "#key" suffix is ignored, since
// checking a specific key would require fetching and parsing the value,
// defeating the point of a cheap, value-free check.
func validateSecret(ctx context.Context, client secretsManagerClient, ref string) *providerproto.ErrorPayload {
	secretID, _ := splitJSONKey(ref)
	_, err := client.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: aws.String(secretID)})
	if err != nil {
		return &providerproto.ErrorPayload{Kind: classify(err), Message: formatAWSError(err)}
	}
	return nil
}

// splitJSONKey splits ref into a secret/parameter ID and an optional JSON
// key selector after the first "#". No selector returns key == "".
func splitJSONKey(ref string) (id, key string) {
	if i := strings.Index(ref, "#"); i >= 0 {
		return ref[:i], ref[i+1:]
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

// parameterStoreClient is the subset of *ssm.Client that resolveParameter
// and validateParameter use, narrowed to an interface purely so tests can
// supply a fake instead of talking to real AWS.
type parameterStoreClient interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	DescribeParameters(ctx context.Context, params *ssm.DescribeParametersInput, optFns ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
}

// resolveParameter fetches a parameter, decrypting it if it's a
// SecureString (a no-op for a plain String parameter). ref may carry a
// "#<key>" suffix to select one field out of a parameter value that holds a
// JSON object, same as resolveSecret.
func resolveParameter(ctx context.Context, client parameterStoreClient, ref string) (providerproto.ResolveResult, *providerproto.ErrorPayload) {
	name, jsonKey := splitJSONKey(ref)

	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
	if err != nil {
		return providerproto.ResolveResult{}, &providerproto.ErrorPayload{Kind: classify(err), Message: formatAWSError(err)}
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return providerproto.ResolveResult{}, &providerproto.ErrorPayload{Kind: providerproto.KindNotFound, Message: "parameter has no value"}
	}
	if jsonKey == "" {
		return providerproto.ResolveResult{Value: *out.Parameter.Value}, nil
	}
	value, errPayload := extractJSONKey(*out.Parameter.Value, jsonKey)
	if errPayload != nil {
		return providerproto.ResolveResult{}, errPayload
	}
	return providerproto.ResolveResult{Value: value}, nil
}

// validateParameter uses DescribeParameters with an exact-name filter,
// which never returns a parameter's value, as the cheaper existence check
// the Provider.Validate contract calls for. A nil return means success. A
// "#key" suffix is ignored, for the same reason as validateSecret.
func validateParameter(ctx context.Context, client parameterStoreClient, ref string) *providerproto.ErrorPayload {
	name, _ := splitJSONKey(ref)
	out, err := client.DescribeParameters(ctx, &ssm.DescribeParametersInput{
		ParameterFilters: []ssmtypes.ParameterStringFilter{
			{Key: aws.String("Name"), Values: []string{name}},
		},
	})
	if err != nil {
		return &providerproto.ErrorPayload{Kind: classify(err), Message: formatAWSError(err)}
	}
	if len(out.Parameters) == 0 {
		return &providerproto.ErrorPayload{Kind: providerproto.KindNotFound, Message: "parameter not found"}
	}
	return nil
}

// formatAWSError renders a clean "<code>: <message>" string for an AWS SDK
// error by extracting just the modeled error code/message, instead of the
// SDK's often deeply-nested operation/transport wrapper chain (e.g.
// "operation error Secrets Manager: GetSecretValue, https response error
// StatusCode: 400, RequestID: ..., api error ValidationException: ..."). A
// non-API error (network failure, missing credentials, ...) falls back to
// its own message, which is already reasonably clear.
func formatAWSError(err error) string {
	apiErr, ok := errors.AsType[smithy.APIError](err)
	if !ok {
		return err.Error()
	}
	code, msg := apiErr.ErrorCode(), apiErr.ErrorMessage()
	switch {
	case code != "" && msg != "":
		return code + ": " + msg
	case code != "":
		return code
	case msg != "":
		return msg
	default:
		return err.Error()
	}
}

// classify maps an AWS SDK error to a protocol Kind via its modeled error
// code, since IAM-level access denials commonly arrive as a generic smithy
// API error rather than a service-specific typed exception. Covers both
// Secrets Manager and SSM Parameter Store error codes. Anything not
// explicitly recognized (throttling, 5xx, network errors, ...) falls back
// to transient, which is the safe default for "retry later" behavior.
func classify(err error) providerproto.Kind {
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "ResourceNotFoundException", "ParameterNotFound":
			return providerproto.KindNotFound
		case "AccessDeniedException", "UnauthorizedException", "AuthFailure":
			return providerproto.KindAccessDenied
		case "InvalidRequestException", "InvalidParameterException", "InvalidKeyId":
			return providerproto.KindInvalidRef
		}
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
