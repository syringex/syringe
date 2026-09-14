package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"

	"github.com/syringex/syringe/pkg/providerproto"
)

// fakeSMClient is a minimal secretsManagerClient for testing resolve and
// validate without a real AWS call.
type fakeSMClient struct {
	getSecretValueOut *secretsmanager.GetSecretValueOutput
	getSecretValueErr error
	describeSecretErr error

	lastSecretID string // captures what was actually sent to AWS
}

func (f *fakeSMClient) GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.lastSecretID = *params.SecretId
	return f.getSecretValueOut, f.getSecretValueErr
}

func (f *fakeSMClient) DescribeSecret(ctx context.Context, params *secretsmanager.DescribeSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error) {
	if f.describeSecretErr != nil {
		return nil, f.describeSecretErr
	}
	return &secretsmanager.DescribeSecretOutput{}, nil
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want providerproto.Kind
	}{
		{"not found", &smithy.GenericAPIError{Code: "ResourceNotFoundException"}, providerproto.KindNotFound},
		{"access denied", &smithy.GenericAPIError{Code: "AccessDeniedException"}, providerproto.KindAccessDenied},
		{"unauthorized", &smithy.GenericAPIError{Code: "UnauthorizedException"}, providerproto.KindAccessDenied},
		{"invalid request", &smithy.GenericAPIError{Code: "InvalidRequestException"}, providerproto.KindInvalidRef},
		{"invalid parameter", &smithy.GenericAPIError{Code: "InvalidParameterException"}, providerproto.KindInvalidRef},
		{"throttling falls back to transient", &smithy.GenericAPIError{Code: "ThrottlingException"}, providerproto.KindTransient},
		{"non-api error falls back to transient", errors.New("network unreachable"), providerproto.KindTransient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.err); got != c.want {
				t.Errorf("classify(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func TestFormatAWSError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"code and message", &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "Secrets Manager can't find the specified secret."}, "ResourceNotFoundException: Secrets Manager can't find the specified secret."},
		{"code only", &smithy.GenericAPIError{Code: "AccessDeniedException"}, "AccessDeniedException"},
		{"message only", &smithy.GenericAPIError{Message: "something went wrong"}, "something went wrong"},
		{"non-API error falls back to Error()", errors.New("dial tcp: connection refused"), "dial tcp: connection refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatAWSError(c.err); got != c.want {
				t.Errorf("formatAWSError(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func TestResolveSecretString(t *testing.T) {
	client := &fakeSMClient{getSecretValueOut: &secretsmanager.GetSecretValueOutput{
		SecretString: strPtr("hunter2"),
	}}
	res, errPayload := resolveSecret(context.Background(), client, "prod/db/password")
	if errPayload != nil {
		t.Fatalf("resolve returned error: %+v", errPayload)
	}
	if res.Value != "hunter2" {
		t.Errorf("Value = %q, want %q", res.Value, "hunter2")
	}
}

func TestResolveSecretBinaryIsBase64Encoded(t *testing.T) {
	client := &fakeSMClient{getSecretValueOut: &secretsmanager.GetSecretValueOutput{
		SecretBinary: []byte("binary-secret"),
	}}
	res, errPayload := resolveSecret(context.Background(), client, "prod/binary-secret")
	if errPayload != nil {
		t.Fatalf("resolve returned error: %+v", errPayload)
	}
	if got, want := res.Value, "YmluYXJ5LXNlY3JldA=="; got != want {
		t.Errorf("Value = %q, want base64 %q", got, want)
	}
}

func TestResolveSecretJSONKey(t *testing.T) {
	client := &fakeSMClient{getSecretValueOut: &secretsmanager.GetSecretValueOutput{
		SecretString: strPtr(`{"username":"admin","password":"hunter2","port":5432,"tls":true}`),
	}}

	res, errPayload := resolveSecret(context.Background(), client, "prod/db/credentials#password")
	if errPayload != nil {
		t.Fatalf("resolve returned error: %+v", errPayload)
	}
	if res.Value != "hunter2" {
		t.Errorf("Value = %q, want %q", res.Value, "hunter2")
	}
	if client.lastSecretID != "prod/db/credentials" {
		t.Errorf("secret ID sent to AWS = %q, want the #key suffix stripped", client.lastSecretID)
	}

	// Non-string JSON values are re-encoded to their JSON representation.
	res, errPayload = resolveSecret(context.Background(), client, "prod/db/credentials#port")
	if errPayload != nil {
		t.Fatalf("resolve returned error: %+v", errPayload)
	}
	if res.Value != "5432" {
		t.Errorf("Value = %q, want %q", res.Value, "5432")
	}
}

func TestResolveSecretJSONKeyNotFound(t *testing.T) {
	client := &fakeSMClient{getSecretValueOut: &secretsmanager.GetSecretValueOutput{
		SecretString: strPtr(`{"username":"admin"}`),
	}}
	_, errPayload := resolveSecret(context.Background(), client, "prod/db/credentials#password")
	if errPayload == nil {
		t.Fatal("expected an error for a key not present in the JSON secret")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

// A JSON null must not be handed back as the four-character string "null" —
// that would silently satisfy a required secret with a bogus value instead
// of surfacing a clear error.
func TestResolveSecretJSONKeyNullIsNotFound(t *testing.T) {
	client := &fakeSMClient{getSecretValueOut: &secretsmanager.GetSecretValueOutput{
		SecretString: strPtr(`{"username":"admin","password":null}`),
	}}
	_, errPayload := resolveSecret(context.Background(), client, "prod/db/credentials#password")
	if errPayload == nil {
		t.Fatal("expected an error for a null value, not a resolved \"null\" string")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

func TestResolveSecretJSONKeyOnNonJSONValue(t *testing.T) {
	client := &fakeSMClient{getSecretValueOut: &secretsmanager.GetSecretValueOutput{
		SecretString: strPtr("just-a-plain-string"),
	}}
	_, errPayload := resolveSecret(context.Background(), client, "prod/db/password#username")
	if errPayload == nil {
		t.Fatal("expected an error when a key is requested but the secret isn't a JSON object")
	}
	if errPayload.Kind != providerproto.KindInvalidRef {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindInvalidRef)
	}
}

func TestResolveSecretJSONKeyOnBinarySecret(t *testing.T) {
	client := &fakeSMClient{getSecretValueOut: &secretsmanager.GetSecretValueOutput{
		SecretBinary: []byte("binary-secret"),
	}}
	_, errPayload := resolveSecret(context.Background(), client, "prod/binary-secret#key")
	if errPayload == nil {
		t.Fatal("expected an error when a key is requested on a binary secret")
	}
	if errPayload.Kind != providerproto.KindInvalidRef {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindInvalidRef)
	}
}

func TestSplitJSONKey(t *testing.T) {
	cases := []struct {
		ref     string
		wantID  string
		wantKey string
	}{
		{"prod/db/password", "prod/db/password", ""},
		{"prod/db/credentials#username", "prod/db/credentials", "username"},
		{"prod/db/credentials#a#b", "prod/db/credentials", "a#b"}, // only the first # splits
	}
	for _, c := range cases {
		id, key := splitJSONKey(c.ref)
		if id != c.wantID || key != c.wantKey {
			t.Errorf("splitJSONKey(%q) = (%q, %q), want (%q, %q)", c.ref, id, key, c.wantID, c.wantKey)
		}
	}
}

func TestResolveNeitherStringNorBinary(t *testing.T) {
	client := &fakeSMClient{getSecretValueOut: &secretsmanager.GetSecretValueOutput{}}
	_, errPayload := resolveSecret(context.Background(), client, "prod/empty")
	if errPayload == nil {
		t.Fatal("expected an error when the secret has neither a string nor binary value")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

func TestResolvePropagatesClassifiedError(t *testing.T) {
	client := &fakeSMClient{getSecretValueErr: &smithy.GenericAPIError{Code: "AccessDeniedException"}}
	_, errPayload := resolveSecret(context.Background(), client, "prod/denied")
	if errPayload == nil {
		t.Fatal("expected an error")
	}
	if errPayload.Kind != providerproto.KindAccessDenied {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindAccessDenied)
	}
}

func TestValidateSecretSuccessAndFailure(t *testing.T) {
	ok := &fakeSMClient{}
	if errPayload := validateSecret(context.Background(), ok, "prod/db/password"); errPayload != nil {
		t.Errorf("validateSecret returned error: %+v", errPayload)
	}

	denied := &fakeSMClient{describeSecretErr: &smithy.GenericAPIError{Code: "ResourceNotFoundException"}}
	errPayload := validateSecret(context.Background(), denied, "prod/missing")
	if errPayload == nil {
		t.Fatal("expected an error")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

// fakeSSMClient is a minimal parameterStoreClient for testing
// resolveParameter and validateParameter without a real AWS call.
type fakeSSMClient struct {
	getParameterOut       *ssm.GetParameterOutput
	getParameterErr       error
	describeParametersOut *ssm.DescribeParametersOutput
	describeParametersErr error
}

func (f *fakeSSMClient) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return f.getParameterOut, f.getParameterErr
}

func (f *fakeSSMClient) DescribeParameters(ctx context.Context, params *ssm.DescribeParametersInput, optFns ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error) {
	if f.describeParametersErr != nil {
		return nil, f.describeParametersErr
	}
	if f.describeParametersOut != nil {
		return f.describeParametersOut, nil
	}
	return &ssm.DescribeParametersOutput{}, nil
}

func TestResolveParameterSuccess(t *testing.T) {
	client := &fakeSSMClient{getParameterOut: &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: strPtr("api-key-value")},
	}}
	res, errPayload := resolveParameter(context.Background(), client, "/myapp/api-key")
	if errPayload != nil {
		t.Fatalf("resolveParameter returned error: %+v", errPayload)
	}
	if res.Value != "api-key-value" {
		t.Errorf("Value = %q, want %q", res.Value, "api-key-value")
	}
}

func TestResolveParameterJSONKey(t *testing.T) {
	client := &fakeSSMClient{getParameterOut: &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: strPtr(`{"username":"admin","password":"hunter2"}`)},
	}}
	res, errPayload := resolveParameter(context.Background(), client, "/myapp/db-credentials#username")
	if errPayload != nil {
		t.Fatalf("resolveParameter returned error: %+v", errPayload)
	}
	if res.Value != "admin" {
		t.Errorf("Value = %q, want %q", res.Value, "admin")
	}
}

func TestResolveParameterMissingValue(t *testing.T) {
	client := &fakeSSMClient{getParameterOut: &ssm.GetParameterOutput{}}
	_, errPayload := resolveParameter(context.Background(), client, "/myapp/empty")
	if errPayload == nil {
		t.Fatal("expected an error when the parameter has no value")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

func TestResolveParameterPropagatesClassifiedError(t *testing.T) {
	client := &fakeSSMClient{getParameterErr: &smithy.GenericAPIError{Code: "ParameterNotFound"}}
	_, errPayload := resolveParameter(context.Background(), client, "/myapp/missing")
	if errPayload == nil {
		t.Fatal("expected an error")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

func TestValidateParameterSuccessAndFailure(t *testing.T) {
	ok := &fakeSSMClient{describeParametersOut: &ssm.DescribeParametersOutput{
		Parameters: []ssmtypes.ParameterMetadata{{}},
	}}
	if errPayload := validateParameter(context.Background(), ok, "/myapp/api-key"); errPayload != nil {
		t.Errorf("validateParameter returned error: %+v", errPayload)
	}

	missing := &fakeSSMClient{} // empty Parameters list
	errPayload := validateParameter(context.Background(), missing, "/myapp/missing")
	if errPayload == nil {
		t.Fatal("expected an error when no parameter matches")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

func strPtr(s string) *string { return &s }

func TestChooseRegion(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		current string
		want    string
		wantErr bool
	}{
		{"typed value wins over current", "eu-west-1", "us-east-1", "eu-west-1", false},
		{"blank input falls back to current", "", "us-east-1", "us-east-1", false},
		{"blank input and no current is an error", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := chooseRegion(c.input, c.current)
			if c.wantErr {
				if err == nil {
					t.Fatalf("chooseRegion(%q, %q) = %q, nil; want an error", c.input, c.current, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("chooseRegion(%q, %q) returned error: %v", c.input, c.current, err)
			}
			if got != c.want {
				t.Errorf("chooseRegion(%q, %q) = %q, want %q", c.input, c.current, got, c.want)
			}
		})
	}
}

func TestRunInitRequiresInteractiveTerminal(t *testing.T) {
	_, errPayload := runInit(context.Background(), strings.NewReader(""), io.Discard, false)
	if errPayload == nil {
		t.Fatal("expected an error when not running interactively")
	}
	if errPayload.Kind != providerproto.KindInvalidRef {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindInvalidRef)
	}
}

func TestRunInitPromptsForProfileAndRegion(t *testing.T) {
	in := strings.NewReader("myprofile\nus-west-2\n")
	env, errPayload := runInit(context.Background(), in, io.Discard, true)
	if errPayload != nil {
		t.Fatalf("runInit returned error: %+v", errPayload)
	}
	if env["AWS_PROFILE"] != "myprofile" {
		t.Errorf("AWS_PROFILE = %q, want %q", env["AWS_PROFILE"], "myprofile")
	}
	if env["AWS_REGION"] != "us-west-2" {
		t.Errorf("AWS_REGION = %q, want %q", env["AWS_REGION"], "us-west-2")
	}
}

func TestRunInitBlankProfileMeansNoOverride(t *testing.T) {
	in := strings.NewReader("\nus-west-2\n")
	env, errPayload := runInit(context.Background(), in, io.Discard, true)
	if errPayload != nil {
		t.Fatalf("runInit returned error: %+v", errPayload)
	}
	if _, ok := env["AWS_PROFILE"]; ok {
		t.Errorf("expected no AWS_PROFILE override for blank input, got %v", env)
	}
	if env["AWS_REGION"] != "us-west-2" {
		t.Errorf("AWS_REGION = %q, want %q", env["AWS_REGION"], "us-west-2")
	}
}

func TestRunInitBlankRegionWithNoResolvableDefaultFails(t *testing.T) {
	// A profile name that (almost certainly) doesn't exist on any machine
	// running this test, so resolveRegion finds nothing to hint, and a
	// blank region answer has nothing to fall back to.
	in := strings.NewReader("inject-test-nonexistent-profile-xyz\n\n")
	_, errPayload := runInit(context.Background(), in, io.Discard, true)
	if errPayload == nil {
		t.Fatal("expected an error when no region can be determined")
	}
}
