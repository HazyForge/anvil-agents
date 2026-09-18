package substrate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/hazyforge/anvil-agents/internal/substrate/ateapipb"
)

// bufconnATEControlServer is an in-memory ateapi Control server speaking real
// gRPC wire protocol. It models the server semantics the mapping depends on:
// unknown actors fail NotFound on every RPC, duplicate creates fail
// AlreadyExists, and Resume reports whether a resume workflow ran.
type bufconnATEControlServer struct {
	ateapipb.UnimplementedControlServer

	mu     sync.Mutex
	actors map[string]*ateapipb.Actor
	// creates records create payloads for template/placement assertions.
	creates []*ateapipb.Actor
}

func newBufconnATEControlServer() *bufconnATEControlServer {
	return &bufconnATEControlServer{actors: map[string]*ateapipb.Actor{}}
}

func bufconnActorKey(atespace, name string) string {
	return strings.TrimSpace(atespace) + "/" + strings.TrimSpace(name)
}

func (s *bufconnATEControlServer) GetActor(_ context.Context, req *ateapipb.GetActorRequest) (*ateapipb.Actor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := bufconnActorKey(req.GetActor().GetAtespace(), req.GetActor().GetName())
	actor, ok := s.actors[key]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "actor %s not found", key)
	}
	return actor, nil
}

func (s *bufconnATEControlServer) CreateActor(_ context.Context, req *ateapipb.CreateActorRequest) (*ateapipb.Actor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := req.GetActor()
	key := bufconnActorKey(want.GetMetadata().GetAtespace(), want.GetMetadata().GetName())
	if _, ok := s.actors[key]; ok {
		return nil, status.Errorf(codes.AlreadyExists, "actor %s already exists", key)
	}
	actor := &ateapipb.Actor{
		Metadata: &ateapipb.ResourceMetadata{
			Atespace: want.GetMetadata().GetAtespace(),
			Name:     want.GetMetadata().GetName(),
			Uid:      "ate-uid-wire-1",
		},
		ActorTemplate:  want.GetActorTemplate(),
		Status:         &ateapipb.ActorStatus{State: ateapipb.ActorState_ACTOR_STATE_RUNNING},
	}
	if want.GetWorkerSelector() != nil {
		actor.WorkerSelector = &ateapipb.Selector{MatchLabels: want.GetWorkerSelector().GetMatchLabels()}
	}
	s.actors[key] = actor
	s.creates = append(s.creates, actor)
	return actor, nil
}

func (s *bufconnATEControlServer) ResumeActor(_ context.Context, req *ateapipb.ResumeActorRequest) (*ateapipb.ResumeActorResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := bufconnActorKey(req.GetActor().GetAtespace(), req.GetActor().GetName())
	actor, ok := s.actors[key]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "actor %s not found", key)
	}
	if actor.GetStatus().GetState() == ateapipb.ActorState_ACTOR_STATE_RUNNING {
		return &ateapipb.ResumeActorResponse{Actor: actor}, nil
	}
	actor.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	return &ateapipb.ResumeActorResponse{Actor: actor, Resumed: true}, nil
}

func (s *bufconnATEControlServer) SuspendActor(_ context.Context, req *ateapipb.SuspendActorRequest) (*ateapipb.SuspendActorResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := bufconnActorKey(req.GetActor().GetAtespace(), req.GetActor().GetName())
	actor, ok := s.actors[key]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "actor %s not found", key)
	}
	actor.Status.State = ateapipb.ActorState_ACTOR_STATE_SUSPENDED
	return &ateapipb.SuspendActorResponse{Actor: actor}, nil
}

func (s *bufconnATEControlServer) PauseActor(_ context.Context, req *ateapipb.PauseActorRequest) (*ateapipb.PauseActorResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := bufconnActorKey(req.GetActor().GetAtespace(), req.GetActor().GetName())
	actor, ok := s.actors[key]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "actor %s not found", key)
	}
	actor.Status.State = ateapipb.ActorState_ACTOR_STATE_PAUSED
	return &ateapipb.PauseActorResponse{Actor: actor}, nil
}

// dialBufconnATEControl serves the in-memory implementation over bufconn and
// returns the seam adapter plus cleanup.
func dialBufconnATEControl(t *testing.T, server *bufconnATEControlServer) (ATEControl, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	ateapipb.RegisterControlServer(srv, server)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("bufconn dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &grpcATEControl{client: ateapipb.NewControlClient(conn)}, func() {}
}

func TestNormalizeATEEndpoint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"ate-api-server.ate-system.svc:443", "ate-api-server.ate-system.svc:443"},
		{"  127.0.0.1:8080  ", "127.0.0.1:8080"},
		{"https://127.0.0.1:8080", "127.0.0.1:8080"},
		{"http://ate-api-server.ate-system.svc:443", "ate-api-server.ate-system.svc:443"},
	} {
		got, err := normalizeATEEndpoint(tc.raw)
		if err != nil {
			t.Fatalf("normalize %q: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("normalize %q = %q, want %q", tc.raw, got, tc.want)
		}
	}
	for _, raw := range []string{"", "   ", "http://", "ate-api/x"} {
		if _, err := normalizeATEEndpoint(raw); err == nil {
			t.Fatalf("normalize %q must fail", raw)
		}
	}
}

func TestIsLoopbackEndpoint(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{"127.0.0.1:8080", "localhost:443", "[::1]:8080", "127.0.0.1", "localhost"} {
		if !isLoopbackEndpoint(endpoint) {
			t.Fatalf("endpoint %q must count as loopback", endpoint)
		}
	}
	for _, endpoint := range []string{"ate-api-server.ate-system.svc:443", "10.0.0.1:443", "example.com:443", ""} {
		if isLoopbackEndpoint(endpoint) {
			t.Fatalf("endpoint %q must not count as loopback", endpoint)
		}
	}
}

func TestLoadTokenFromFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("  secret-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	got, err := loadTokenFromFile(path)
	if err != nil {
		t.Fatalf("load token: %v", err)
	}
	if got != "secret-token" {
		t.Fatalf("token = %q, want trimmed secret-token", got)
	}
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	if _, err := loadTokenFromFile(path); err == nil {
		t.Fatal("empty token file must fail fast")
	}
	if _, err := loadTokenFromFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing token file must fail")
	}
}

func TestMapGRPCError(t *testing.T) {
	t.Parallel()

	if err := mapGRPCError(nil); err != nil {
		t.Fatalf("nil maps to %v, want nil", err)
	}
	notFound := mapGRPCError(status.Error(codes.NotFound, "missing"))
	if !IsATENotFound(notFound) {
		t.Fatalf("NotFound maps to %v, want IsATENotFound", notFound)
	}
	if !errors.Is(notFound, status.Error(codes.NotFound, "missing")) && !strings.Contains(notFound.Error(), "missing") {
		t.Fatalf("NotFound must preserve the server message: %v", notFound)
	}
	alreadyExists := mapGRPCError(status.Error(codes.AlreadyExists, "dup"))
	if !IsATEAlreadyExists(alreadyExists) {
		t.Fatalf("AlreadyExists maps to %v, want IsATEAlreadyExists", alreadyExists)
	}
	other := mapGRPCError(status.Error(codes.Unavailable, "down"))
	var ateErr *ATEError
	if !errors.As(other, &ateErr) || ateErr.Code != ATECodeOther {
		t.Fatalf("Unavailable maps to %v, want ATECodeOther", other)
	}
}

func TestAteStateFromProtoFoldsThroughMapATEState(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		wire ateapipb.ActorState
		want ActorState
	}{
		{ateapipb.ActorState_ACTOR_STATE_RUNNING, ActorStateActive},
		{ateapipb.ActorState_ACTOR_STATE_RESUMING, ActorStateActive},
		{ateapipb.ActorState_ACTOR_STATE_SUSPENDED, ActorStateSuspended},
		{ateapipb.ActorState_ACTOR_STATE_SUSPENDING, ActorStateSuspended},
		{ateapipb.ActorState_ACTOR_STATE_CRASHED, ActorStateSuspended},
		{ateapipb.ActorState_ACTOR_STATE_REVERTING, ActorStateSuspended},
		{ateapipb.ActorState_ACTOR_STATE_PAUSED, ActorStatePaused},
		{ateapipb.ActorState_ACTOR_STATE_PAUSING, ActorStatePaused},
		{ateapipb.ActorState_ACTOR_STATE_DELETING, ActorStateActive},
		{ateapipb.ActorState_ACTOR_STATE_UNSPECIFIED, ActorStateActive},
	} {
		if got := MapATEState(ateStateFromProto(tc.wire)); got != tc.want {
			t.Fatalf("wire %v folds to %q, want %q", tc.wire, got, tc.want)
		}
	}
	// Transitional states stay distinct on the seam so callers can observe
	// them before folding.
	if got := ateStateFromProto(ateapipb.ActorState_ACTOR_STATE_RESUMING); got != ATEActorStateResuming {
		t.Fatalf("resuming seam state = %q, want Resuming", got)
	}
	if got := ateStateFromProto(ateapipb.ActorState_ACTOR_STATE_REVERTING); got != ATEActorStateReverting {
		t.Fatalf("reverting seam state = %q, want Reverting", got)
	}
	if got := ateStateFromProto(ateapipb.ActorState(9)); got != ATEActorStateReverting {
		t.Fatalf("wire 9 seam state = %q, want Reverting", got)
	}
}

func TestProtoCreateActorCarriesTemplateAndPool(t *testing.T) {
	t.Parallel()

	spec := ATECreateSpec{Atespace: "agents", Name: "chat-1", TemplateAtespace: "agents", TemplateName: "standing-chat", WorkerSelector: map[string]string{WorkerSelectorPoolLabel: "warm"}}
	pb := protoCreateActor(spec)
	if pb.GetMetadata().GetAtespace() != "agents" || pb.GetMetadata().GetName() != "chat-1" {
		t.Fatalf("wire identity = %+v, want agents/chat-1", pb.GetMetadata())
	}
	if pb.GetActorTemplate().GetAtespace() != "agents" || pb.GetActorTemplate().GetName() != "standing-chat" {
		t.Fatalf("wire template = %+v, want agents/standing-chat", pb.GetActorTemplate())
	}
	if pb.GetWorkerSelector().GetMatchLabels()[WorkerSelectorPoolLabel] != "warm" {
		t.Fatalf("wire selector = %+v, want pool warm", pb.GetWorkerSelector())
	}
}

func TestGRPCControlWireRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server := newBufconnATEControlServer()
	control, _ := dialBufconnATEControl(t, server)
	client, err := NewATEClient(ATEClientConfig{Address: "bufnet", Template: "standing-chat"}, control)
	if err != nil {
		t.Fatalf("NewATEClient: %v", err)
	}

	created, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1", ActorClass: "standing-chat", Pool: "warm"})
	if err != nil {
		t.Fatalf("create over the wire: %v", err)
	}
	if created.State != ActorStateActive || created.ID == "" {
		t.Fatalf("created handle = %+v, want active with a server UID", created)
	}
	if len(server.creates) != 1 || server.creates[0].GetActorTemplate().GetName() != "standing-chat" {
		t.Fatalf("server creates = %+v, want one standing-chat create", server.creates)
	}
	if server.creates[0].GetWorkerSelector().GetMatchLabels()[WorkerSelectorPoolLabel] != "warm" {
		t.Fatalf("server selector = %+v, want pool warm", server.creates[0].GetWorkerSelector())
	}

	if _, err := client.SuspendActor(ctx, "agents", "chat-thread-1"); err != nil {
		t.Fatalf("suspend over the wire: %v", err)
	}
	resumed, err := client.ResumeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("resume over the wire: %v", err)
	}
	if resumed.Resumes != 1 {
		t.Fatalf("resumed handle = %+v, want one observed resume workflow", resumed)
	}
	// Warm reuse: re-create returns the same server UID with the count kept.
	second, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"})
	if err != nil {
		t.Fatalf("re-create over the wire: %v", err)
	}
	if second.ID != created.ID || second.Resumes != 1 {
		t.Fatalf("re-created handle = %+v, want warm reuse of %+v", second, created)
	}
	if _, err := client.PauseActor(ctx, "agents", "chat-thread-1"); err != nil {
		t.Fatalf("pause over the wire: %v", err)
	}
	described, err := client.DescribeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("describe over the wire: %v", err)
	}
	if described.State != ActorStatePaused {
		t.Fatalf("described state = %q, want Paused", described.State)
	}
	if _, err := client.ResumeActor(ctx, "agents", "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("resume missing = %v, want ErrActorNotFound", err)
	}
}

func TestDialInsecureRefusesNonLoopback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := ATEClientConfig{Address: "ate-api-server.ate-system.svc:443", Template: "standing-chat", Insecure: true}
	if _, _, err := DialATEControl(ctx, cfg, ATEDialOptions{}); err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("insecure non-loopback dial err = %v, want a non-loopback refusal", err)
	}
}

// TestDialInsecureLoopbackRoundTrip proves the real dial path (skip-verify
// TLS over a loopback TCP listener, since ateapi always serves TLS) via the
// same DialATEControl the controller and the latency harness use.
func TestDialInsecureLoopbackRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server := newBufconnATEControlServer()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// ateapi always serves TLS, so the loopback fixture serves TLS with a
	// throwaway self-signed cert; the insecure dial skips verification.
	serverCert := loopbackServerCert(t)
	srv := grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&serverCert)))
	ateapipb.RegisterControlServer(srv, server)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	cfg := ATEClientConfig{Address: lis.Addr().String(), Template: "standing-chat", Insecure: true}
	control, closeConn, err := DialATEControl(ctx, cfg, ATEDialOptions{})
	if err != nil {
		t.Fatalf("DialATEControl insecure loopback: %v", err)
	}
	defer closeConn()
	client, err := NewATEClient(cfg, control)
	if err != nil {
		t.Fatalf("NewATEClient: %v", err)
	}
	created, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-dial-1", ActorClass: "standing-chat"})
	if err != nil {
		t.Fatalf("create via dialed client: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created handle = %+v, want a server UID", created)
	}
	if _, err := client.SuspendActor(ctx, "agents", "chat-dial-1"); err != nil {
		t.Fatalf("suspend via dialed client: %v", err)
	}
	resumed, err := client.ResumeActor(ctx, "agents", "chat-dial-1")
	if err != nil {
		t.Fatalf("resume via dialed client: %v", err)
	}
	if resumed.Resumes != 1 {
		t.Fatalf("resumed handle = %+v, want one observed resume", resumed)
	}
	// A file token on the insecure path must still attach over the TLS
	// channel (per-RPC creds require transport security).
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("loopback-token"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	fileCfg := ATEClientConfig{Address: lis.Addr().String(), Template: "standing-chat", Insecure: true, TokenFile: tokenPath}
	fileControl, closeFile, err := DialATEControl(ctx, fileCfg, ATEDialOptions{})
	if err != nil {
		t.Fatalf("DialATEControl insecure loopback with token file: %v", err)
	}
	defer closeFile()
	fileClient, err := NewATEClient(fileCfg, fileControl)
	if err != nil {
		t.Fatalf("NewATEClient with token file: %v", err)
	}
	if _, err := fileClient.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-dial-2", ActorClass: "standing-chat"}); err != nil {
		t.Fatalf("create via token-file dialed client: %v", err)
	}
}

// loopbackServerCert mints a throwaway self-signed TLS cert for the insecure
// (skip-verify) loopback fixture. ateapi always serves TLS, so the fixture
// must too; the client skips verification.
func loopbackServerCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ate-loopback-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		mustMarshalECKey(t, key),
	)
	if err != nil {
		t.Fatalf("marshal key pair: %v", err)
	}
	return cert
}

func mustMarshalECKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal EC key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func TestServerTLSConfigRequiresLiveBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clientset := kubefake.NewSimpleClientset()
	if _, err := serverTLSConfig(ctx, clientset); err == nil || !strings.Contains(err.Error(), "ClusterTrustBundle") {
		t.Fatalf("TLS without bundles err = %v, want a ClusterTrustBundle error", err)
	}
	wrongSigner := &certificatesv1beta1.ClusterTrustBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "wrong", Labels: map[string]string{"podcert.ate.dev/canarying": "live"}},
		Spec:       certificatesv1beta1.ClusterTrustBundleSpec{SignerName: "other.example/signer", TrustBundle: "junk"},
	}
	clientset = kubefake.NewSimpleClientset(wrongSigner)
	if _, err := serverTLSConfig(ctx, clientset); err == nil || !strings.Contains(err.Error(), "ClusterTrustBundle") {
		t.Fatalf("TLS with wrong signer err = %v, want a ClusterTrustBundle error", err)
	}
}

func TestServerTLSConfigAcceptsLiveBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	caPEM := selfSignedCAPEM(t)
	bundle := &certificatesv1beta1.ClusterTrustBundle{
		ObjectMeta: metav1.ObjectMeta{Name: "live", Labels: map[string]string{"podcert.ate.dev/canarying": "live"}},
		Spec:       certificatesv1beta1.ClusterTrustBundleSpec{SignerName: "servicedns.podcert.ate.dev/identity", TrustBundle: string(caPEM)},
	}
	clientset := kubefake.NewSimpleClientset(bundle)
	tlsCfg, err := serverTLSConfig(ctx, clientset)
	if err != nil {
		t.Fatalf("TLS with live bundle: %v", err)
	}
	if tlsCfg.ServerName != ateAPIServerName || tlsCfg.MinVersion != 0x0304 {
		t.Fatalf("TLS config = %+v, want ServerName %q with TLS 1.3", tlsCfg, ateAPIServerName)
	}
	if len(tlsCfg.RootCAs.Subjects()) == 0 {
		t.Fatal("TLS RootCAs must hold the bundle certificate")
	}
}

func selfSignedCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ate-test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestGateInsecureParsesFromEnv(t *testing.T) {
	t.Setenv(GateEnabledEnvVar, "true")
	t.Setenv(GateEndpointEnvVar, "127.0.0.1:8080")
	t.Setenv(GateTemplateEnvVar, "standing-chat")
	t.Setenv(GateInsecureEnvVar, "true")

	gate := GateConfigFromEnv()
	if !gate.LiveEnabled() || !gate.Insecure {
		t.Fatalf("gate from env = %+v, want live with insecure", gate)
	}
	cfg := ATEClientConfigFromGate(gate)
	if !cfg.Insecure || cfg.Address != "127.0.0.1:8080" {
		t.Fatalf("client config from gate = %+v, want insecure loopback", cfg)
	}
}

func TestStaticBearerCredentials(t *testing.T) {
	t.Parallel()

	creds := staticBearerCreds{token: "abc", requireTLS: true}
	metadata, err := creds.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if metadata["authorization"] != "Bearer abc" {
		t.Fatalf("metadata = %+v, want Bearer abc", metadata)
	}
	if !creds.RequireTransportSecurity() {
		t.Fatal("secure bearer credentials must require transport security")
	}
	if _, err := (staticBearerCreds{}).GetRequestMetadata(context.Background()); err == nil {
		t.Fatal("empty token must fail")
	}
	// The insecure path is still a TLS channel (skip-verify), so even
	// loopback per-RPC creds must require transport security.
	insecureCreds := staticBearerCreds{token: "abc", requireTLS: true}
	if !insecureCreds.RequireTransportSecurity() {
		t.Fatal("insecure-dev credentials must require transport security over skip-verify TLS")
	}
	if got := (fileBearerCreds{path: "some/path", requireTLS: true}).RequireTransportSecurity(); !got {
		t.Fatal("file bearer credentials must require transport security over skip-verify TLS")
	}
}
