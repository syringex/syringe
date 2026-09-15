package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/syringex/syringe/pkg/providerproto"
)

// fakeClient is a minimal secretManagerClient for testing resolve and
// validate without a real GCP call.
type fakeClient struct {
	accessOut *secretmanagerpb.AccessSecretVersionResponse
	accessErr error
	getErr    error

	lastAccessName string // captures what was actually sent to GCP
	lastGetName    string
}

func (f *fakeClient) AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	f.lastAccessName = req.Name
	return f.accessOut, f.accessErr
}

func (f *fakeClient) GetSecretVersion(ctx context.Context, req *secretmanagerpb.GetSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.SecretVersion, error) {
	f.lastGetName = req.Name
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &secretmanagerpb.SecretVersion{Name: req.Name}, nil
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want providerproto.Kind
	}{
		{"not found", status.Error(codes.NotFound, "not found"), providerproto.KindNotFound},
		{"permission denied", status.Error(codes.PermissionDenied, "denied"), providerproto.KindAccessDenied},
		{"unauthenticated", status.Error(codes.Unauthenticated, "no creds"), providerproto.KindAccessDenied},
		{"invalid argument", status.Error(codes.InvalidArgument, "bad name"), providerproto.KindInvalidRef},
		{"failed precondition", status.Error(codes.FailedPrecondition, "disabled"), providerproto.KindInvalidRef},
		{"unavailable falls back to transient", status.Error(codes.Unavailable, "try again"), providerproto.KindTransient},
		{"non-grpc error falls back to transient", errors.New("dial tcp: connection refused"), providerproto.KindTransient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.err); got != c.want {
				t.Errorf("classify(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func TestFormatGCPError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"code and message", status.Error(codes.NotFound, "Secret [x] not found."), "NotFound: Secret [x] not found."},
		{"non-grpc error falls back to Error()", errors.New("dial tcp: connection refused"), "dial tcp: connection refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatGCPError(c.err); got != c.want {
				t.Errorf("formatGCPError(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func TestVersionName(t *testing.T) {
	got := versionName("my-project", "my-secret")
	want := "projects/my-project/secrets/my-secret/versions/latest"
	if got != want {
		t.Errorf("versionName() = %q, want %q", got, want)
	}
}

func TestResolveSecretString(t *testing.T) {
	client := &fakeClient{accessOut: &secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: []byte("hunter2")},
	}}
	res, errPayload := resolveSecret(context.Background(), client, "my-project", "db-password")
	if errPayload != nil {
		t.Fatalf("resolve returned error: %+v", errPayload)
	}
	if res.Value != "hunter2" {
		t.Errorf("Value = %q, want %q", res.Value, "hunter2")
	}
	if want := "projects/my-project/secrets/db-password/versions/latest"; client.lastAccessName != want {
		t.Errorf("resource name sent to GCP = %q, want %q", client.lastAccessName, want)
	}
}

func TestResolveSecretNonUTF8IsBase64Encoded(t *testing.T) {
	client := &fakeClient{accessOut: &secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: []byte{0xff, 0xfe, 0x00, 0x01}},
	}}
	res, errPayload := resolveSecret(context.Background(), client, "my-project", "binary-secret")
	if errPayload != nil {
		t.Fatalf("resolve returned error: %+v", errPayload)
	}
	if got, want := res.Value, "//4AAQ=="; got != want {
		t.Errorf("Value = %q, want base64 %q", got, want)
	}
}

func TestResolveSecretJSONKey(t *testing.T) {
	client := &fakeClient{accessOut: &secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: []byte(`{"username":"admin","password":"hunter2","port":5432,"tls":true}`)},
	}}

	res, errPayload := resolveSecret(context.Background(), client, "my-project", "db-credentials#password")
	if errPayload != nil {
		t.Fatalf("resolve returned error: %+v", errPayload)
	}
	if res.Value != "hunter2" {
		t.Errorf("Value = %q, want %q", res.Value, "hunter2")
	}
	if want := "projects/my-project/secrets/db-credentials/versions/latest"; client.lastAccessName != want {
		t.Errorf("resource name sent to GCP = %q, want the #key suffix stripped: %q", client.lastAccessName, want)
	}

	// Non-string JSON values are re-encoded to their JSON representation.
	res, errPayload = resolveSecret(context.Background(), client, "my-project", "db-credentials#port")
	if errPayload != nil {
		t.Fatalf("resolve returned error: %+v", errPayload)
	}
	if res.Value != "5432" {
		t.Errorf("Value = %q, want %q", res.Value, "5432")
	}
}

func TestResolveSecretJSONKeyNotFound(t *testing.T) {
	client := &fakeClient{accessOut: &secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: []byte(`{"username":"admin"}`)},
	}}
	_, errPayload := resolveSecret(context.Background(), client, "my-project", "db-credentials#password")
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
	client := &fakeClient{accessOut: &secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: []byte(`{"username":"admin","password":null}`)},
	}}
	_, errPayload := resolveSecret(context.Background(), client, "my-project", "db-credentials#password")
	if errPayload == nil {
		t.Fatal("expected an error for a null value, not a resolved \"null\" string")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

func TestResolveSecretJSONKeyOnNonJSONValue(t *testing.T) {
	client := &fakeClient{accessOut: &secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: []byte("just-a-plain-string")},
	}}
	_, errPayload := resolveSecret(context.Background(), client, "my-project", "db-password#username")
	if errPayload == nil {
		t.Fatal("expected an error when a key is requested but the secret isn't a JSON object")
	}
	if errPayload.Kind != providerproto.KindInvalidRef {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindInvalidRef)
	}
}

func TestResolveSecretJSONKeyOnNonUTF8Value(t *testing.T) {
	client := &fakeClient{accessOut: &secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: []byte{0xff, 0xfe}},
	}}
	_, errPayload := resolveSecret(context.Background(), client, "my-project", "binary-secret#key")
	if errPayload == nil {
		t.Fatal("expected an error when a key is requested on a non-text secret")
	}
	if errPayload.Kind != providerproto.KindInvalidRef {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindInvalidRef)
	}
}

func TestResolveSecretNoPayload(t *testing.T) {
	client := &fakeClient{accessOut: &secretmanagerpb.AccessSecretVersionResponse{}}
	_, errPayload := resolveSecret(context.Background(), client, "my-project", "empty")
	if errPayload == nil {
		t.Fatal("expected an error when the secret has no payload")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

func TestResolvePropagatesClassifiedError(t *testing.T) {
	client := &fakeClient{accessErr: status.Error(codes.PermissionDenied, "denied")}
	_, errPayload := resolveSecret(context.Background(), client, "my-project", "denied")
	if errPayload == nil {
		t.Fatal("expected an error")
	}
	if errPayload.Kind != providerproto.KindAccessDenied {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindAccessDenied)
	}
}

func TestSplitJSONKey(t *testing.T) {
	cases := []struct {
		ref     string
		wantID  string
		wantKey string
	}{
		{"db-password", "db-password", ""},
		{"db-credentials#username", "db-credentials", "username"},
		{"db-credentials#a#b", "db-credentials", "a#b"}, // only the first # splits
	}
	for _, c := range cases {
		id, key := splitJSONKey(c.ref)
		if id != c.wantID || key != c.wantKey {
			t.Errorf("splitJSONKey(%q) = (%q, %q), want (%q, %q)", c.ref, id, key, c.wantID, c.wantKey)
		}
	}
}

func TestValidateSecretSuccessAndFailure(t *testing.T) {
	ok := &fakeClient{}
	if errPayload := validateSecret(context.Background(), ok, "my-project", "db-password"); errPayload != nil {
		t.Errorf("validateSecret returned error: %+v", errPayload)
	}
	if want := "projects/my-project/secrets/db-password/versions/latest"; ok.lastGetName != want {
		t.Errorf("resource name sent to GCP = %q, want %q", ok.lastGetName, want)
	}

	missing := &fakeClient{getErr: status.Error(codes.NotFound, "not found")}
	errPayload := validateSecret(context.Background(), missing, "my-project", "missing")
	if errPayload == nil {
		t.Fatal("expected an error")
	}
	if errPayload.Kind != providerproto.KindNotFound {
		t.Errorf("Kind = %q, want %q", errPayload.Kind, providerproto.KindNotFound)
	}
}

func TestChooseProject(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		current string
		want    string
		wantErr bool
	}{
		{"typed value wins over current", "my-other-project", "my-project", "my-other-project", false},
		{"blank input falls back to current", "", "my-project", "my-project", false},
		{"blank input and no current is an error", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := chooseProject(c.input, c.current)
			if c.wantErr {
				if err == nil {
					t.Fatalf("chooseProject(%q, %q) = %q, nil; want an error", c.input, c.current, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("chooseProject(%q, %q) returned error: %v", c.input, c.current, err)
			}
			if got != c.want {
				t.Errorf("chooseProject(%q, %q) = %q, want %q", c.input, c.current, got, c.want)
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

func TestRunInitPromptsForProject(t *testing.T) {
	in := strings.NewReader("my-typed-project\n")
	env, errPayload := runInit(context.Background(), in, io.Discard, true)
	if errPayload != nil {
		t.Fatalf("runInit returned error: %+v", errPayload)
	}
	if env["GOOGLE_CLOUD_PROJECT"] != "my-typed-project" {
		t.Errorf("GOOGLE_CLOUD_PROJECT = %q, want %q", env["GOOGLE_CLOUD_PROJECT"], "my-typed-project")
	}
}

func TestRunInitBlankProjectWithNoResolvableDefaultFails(t *testing.T) {
	// Points GOOGLE_APPLICATION_CREDENTIALS at a nonexistent file so
	// resolveDefaultProject reliably finds nothing to hint, regardless of
	// whatever ADC happens to be ambient on the machine running this test
	// (a real gcloud login, a service account key, ...).
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/inject-test-credentials.json")
	in := strings.NewReader("\n")
	_, errPayload := runInit(context.Background(), in, io.Discard, true)
	if errPayload == nil {
		t.Fatal("expected an error when no project can be determined")
	}
}
