// Live ATE gRPC dialer for the standing-chat actor backend.
//
// This file dials Substrate ATE's real surface (ateapi Control gRPC, the same
// calls cmd/kubectl-ate drives) and adapts it onto the proto-agnostic
// ATEControl seam in live.go. The wire types live in
// internal/substrate/ateapipb, a minimal generated subset of the upstream
// ateapi.proto (see ateapipb/ateapi.proto for the refresh procedure).
//
// TLS/token parity with upstream internal/ateclient (builder.go):
//
//   - TLS is verified BEFORE any bearer token is attached. The serving cert
//     is verified against the live ClusterTrustBundle for signer
//     servicedns.podcert.ate.dev/identity (label
//     podcert.ate.dev/canarying=live), TLS 1.3 minimum, ServerName
//     api.ate-system.svc. The bundle is fetched through the caller's
//     kubeconfig (Kind/local) or in-cluster config (controller), the same
//     sources kubectl-ate uses.
//   - The bearer token comes from ATEClientConfig.TokenFile (kubectl-ate
//     --token-file parallel, "-" reads stdin) or the inline
//     ATEClientConfig.Token, and is attached per-RPC as an authorization
//     header. File tokens are re-read per RPC so rotation works. Token bytes
//     never appear in errors, logs, or status — only the file path does.
//   - With no explicit token the dialer mints a short-lived ServiceAccount
//     token for ate-client in ate-system (audience api.ate-system.svc, 1h),
//     exactly like kubectl-ate's default. In-cluster controllers typically
//     lack that RBAC, so mount a token file there instead.
//   - Round-robin balances across ateapi replicas behind the headless
//     Service, matching upstream.
//
// Kind-only insecure escape hatch: ATEClientConfig.Insecure (gate env
// ANVIL_AGENTS_SUBSTRATE_INSECURE / --substrate-insecure) dials TLS with
// certificate verification skipped (InsecureSkipVerify, TLS 1.3 minimum,
// ServerName api.ate-system.svc). ateapi always serves TLS, so this is still
// a TLS channel — never plaintext/h2c. It refuses every non-loopback
// endpoint, so it can only reach a local port-forward on the developer's own
// machine. This exists for Kind spikes where the podcert trust bundle is not
// yet wired into the local kubeconfig; production and shared clusters must
// use verified TLS.
// The flag, the loopback guard, and this comment are the explicit marker the
// task requires — insecure dial is never silent.
package substrate

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"

	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/hazyforge/anvil-agents/internal/substrate/ateapipb"
)

const (
	// ateAPIServerName is the TLS ServerName ateapi serves, mirrored from
	// upstream internal/ateclient (apiServerName).
	ateAPIServerName = "api.ate-system.svc"
	// ateTokenAudience is the audience minted ServiceAccount tokens assert,
	// mirrored from upstream.
	ateTokenAudience = "api.ate-system.svc"
	// ateTrustBundleSigner and ateTrustBundleSelector identify the live
	// ClusterTrustBundle projected from the podcert signer, mirrored from
	// upstream.
	ateTrustBundleSigner   = "servicedns.podcert.ate.dev/identity"
	ateTrustBundleSelector = "podcert.ate.dev/canarying=live"
	// ateTokenServiceAccount is the "namespace/name" of the ServiceAccount
	// kubectl-ate mints tokens for when --token-file is omitted.
	ateTokenServiceAccountNamespace = "ate-system"
	ateTokenServiceAccountName      = "ate-client"
)

// roundRobinServiceConfig spreads RPCs over every address the resolver
// returns (ateapi is a headless Service). Mirrored from upstream.
const roundRobinServiceConfig = `{"loadBalancingConfig": [{"round_robin":{}}]}`

// ATEDialOptions carries out-of-band dial inputs that are not part of the
// transport config: where to find Kubernetes credentials for ClusterTrustBundle
// verification and ServiceAccount token minting.
type ATEDialOptions struct {
	// KubeconfigPath overrides the standard kubeconfig lookup (KUBECONFIG /
	// ~/.kube/config). Empty means default loading rules; in-cluster config
	// is used when no kubeconfig is available (controller).
	KubeconfigPath string
	// K8sContext selects the kubeconfig context. Empty means the current
	// context.
	K8sContext string
}

// normalizeATEEndpoint trims the configured ateapi target and tolerates an
// http(s):// scheme prefix for copy-paste convenience. The gate endpoint is a
// gRPC target (host:port), e.g. ate-api-server.ate-system.svc:443 in cluster
// or 127.0.0.1:PORT for a Kind port-forward.
func normalizeATEEndpoint(raw string) (string, error) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return "", fmt.Errorf("substrate ATE address is required (see %s)", GateEndpointEnvVar)
	}
	lower := strings.ToLower(endpoint)
	switch {
	case strings.HasPrefix(lower, "http://"):
		endpoint = strings.TrimSpace(endpoint[len("http://"):])
	case strings.HasPrefix(lower, "https://"):
		endpoint = strings.TrimSpace(endpoint[len("https://"):])
	}
	if endpoint == "" {
		return "", fmt.Errorf("substrate ATE address is required (see %s)", GateEndpointEnvVar)
	}
	if strings.Contains(endpoint, "/") {
		return "", fmt.Errorf("substrate ATE address %q must be a host:port target, not a URL", strings.TrimSpace(raw))
	}
	return endpoint, nil
}

// isLoopbackEndpoint reports whether the endpoint resolves to the local
// machine. Used only to gate the insecure-dev dial.
func isLoopbackEndpoint(endpoint string) bool {
	host := strings.TrimSpace(endpoint)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// loadTokenFromFile reads a bearer token from path ("-" reads stdin,
// mirroring kubectl-ate --token-file). The returned string is trimmed;
// empty files are an error so a mis-mounted secret fails fast.
func loadTokenFromFile(path string) (string, error) {
	if strings.TrimSpace(path) == "-" {
		var sb strings.Builder
		buf := make([]byte, 32*1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		token := strings.TrimSpace(sb.String())
		if token == "" {
			return "", fmt.Errorf("bearer token on stdin is empty")
		}
		return token, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read bearer token file %q: %w", path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("bearer token file %q is empty", path)
	}
	return token, nil
}

// staticBearerCreds attaches a fixed bearer token to every RPC.
type staticBearerCreds struct {
	token      string
	requireTLS bool
}

func (c staticBearerCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	if strings.TrimSpace(c.token) == "" {
		return nil, fmt.Errorf("bearer token is empty")
	}
	return map[string]string{"authorization": "Bearer " + strings.TrimSpace(c.token)}, nil
}

func (c staticBearerCreds) RequireTransportSecurity() bool { return c.requireTLS }

// fileBearerCreds re-reads the token file on every RPC so rotated secrets
// take effect without redialing. Only the path (never token bytes) appears in
// errors.
type fileBearerCreds struct {
	path       string
	requireTLS bool
}

func (c fileBearerCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	token, err := loadTokenFromFile(c.path)
	if err != nil {
		return nil, err
	}
	return map[string]string{"authorization": "Bearer " + token}, nil
}

func (c fileBearerCreds) RequireTransportSecurity() bool { return c.requireTLS }

// loadKubeConfig mirrors upstream ateclient.LoadKubeConfig: explicit path and
// context override the standard loading rules.
func loadATEKubeConfig(kubeconfigPath, k8sContext string) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = strings.TrimSpace(kubeconfigPath)
	overrides := &clientcmd.ConfigOverrides{CurrentContext: strings.TrimSpace(k8sContext)}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
}

// ateKubernetesClientset builds a clientset from kubeconfig (Kind/local) or
// in-cluster config (controller), suppressing the certificates.k8s.io/v1beta1
// deprecation warnings the same way upstream does.
func ateKubernetesClientset(opts ATEDialOptions) (*kubernetes.Clientset, error) {
	config, err := loadATEKubeConfig(opts.KubeconfigPath, opts.K8sContext)
	if err != nil {
		inCluster, inErr := rest.InClusterConfig()
		if inErr != nil {
			return nil, fmt.Errorf("load kubeconfig: %w", err)
		}
		config = inCluster
	}
	config.WarningHandlerWithContext = &rest.NoWarnings{}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return clientset, nil
}

// serverTLSConfig builds the verified TLS config from the live podcert
// ClusterTrustBundle. Verification happens before any bearer token is
// attached, so the token never travels over an unauthenticated channel.
func serverTLSConfig(ctx context.Context, clientset kubernetes.Interface) (*tls.Config, error) {
	if clientset == nil {
		return nil, fmt.Errorf("Kubernetes client is required to verify ateapi TLS")
	}
	bundles, err := clientset.CertificatesV1beta1().ClusterTrustBundles().List(ctx, metav1.ListOptions{
		LabelSelector: ateTrustBundleSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list ClusterTrustBundles: %w", err)
	}
	pool := x509.NewCertPool()
	found := false
	for _, bundle := range bundles.Items {
		if bundle.Spec.SignerName != ateTrustBundleSigner {
			continue
		}
		if !pool.AppendCertsFromPEM([]byte(bundle.Spec.TrustBundle)) {
			return nil, fmt.Errorf("ClusterTrustBundle %q contains no valid certificates", bundle.ObjectMeta.Name)
		}
		found = true
	}
	if !found {
		return nil, fmt.Errorf("no live ClusterTrustBundle found for signer %q (is Substrate ATE installed? for a Kind port-forward see docs/substrate-spike.md, or set %s for insecure-dev loopback only)", ateTrustBundleSigner, GateInsecureEnvVar)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: ateAPIServerName,
	}, nil
}

// mintServiceAccountToken requests a short-lived ate-client token from the
// Kubernetes API, mirroring kubectl-ate's default when --token-file is
// omitted.
func mintServiceAccountToken(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	if clientset == nil {
		return "", fmt.Errorf("Kubernetes client is required to mint an ateapi token")
	}
	expiration := int64(3600)
	request := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			Audiences:         []string{ateTokenAudience},
			ExpirationSeconds: &expiration,
		},
	}
	token, err := clientset.CoreV1().ServiceAccounts(ateTokenServiceAccountNamespace).CreateToken(ctx, ateTokenServiceAccountName, request, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("request ateapi bearer token for %s/%s: %w (mount a token file via %s instead)", ateTokenServiceAccountNamespace, ateTokenServiceAccountName, err, GateTokenFileEnvVar)
	}
	if strings.TrimSpace(token.Status.Token) == "" {
		return "", fmt.Errorf("request ateapi bearer token: token response was empty (mount a token file via %s instead)", GateTokenFileEnvVar)
	}
	return strings.TrimSpace(token.Status.Token), nil
}

// resolveBearerCredentials picks the token source: explicit file first,
// inline token second, ServiceAccount mint last (kubectl-ate parity). File
// credentials re-read per RPC; static credentials carry one resolved token.
func resolveBearerCredentials(ctx context.Context, cfg ATEClientConfig, clientset kubernetes.Interface, requireTLS bool) (grpc.DialOption, error) {
	if strings.TrimSpace(cfg.TokenFile) != "" {
		if _, err := loadTokenFromFile(strings.TrimSpace(cfg.TokenFile)); err != nil {
			return nil, err
		}
		return grpc.WithPerRPCCredentials(fileBearerCreds{path: strings.TrimSpace(cfg.TokenFile), requireTLS: requireTLS}), nil
	}
	if strings.TrimSpace(cfg.Token) != "" {
		return grpc.WithPerRPCCredentials(staticBearerCreds{token: strings.TrimSpace(cfg.Token), requireTLS: requireTLS}), nil
	}
	token, err := mintServiceAccountToken(ctx, clientset)
	if err != nil {
		return nil, err
	}
	return grpc.WithPerRPCCredentials(staticBearerCreds{token: token, requireTLS: requireTLS}), nil
}

// mapGRPCError folds gRPC status codes onto the transport-agnostic ATE error
// shape: NotFound -> ErrActorNotFound via IsATENotFound, AlreadyExists ->
// warm-reuse via IsATEAlreadyExists. Unknown-actor NotFound is what ateapi
// returns on every lifecycle RPC; template-missing NotFound on create also
// maps there so callers can distinguish it from transport failures.
func mapGRPCError(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.NotFound:
		return &ATEError{Code: ATECodeNotFound, Err: err}
	case codes.AlreadyExists:
		return &ATEError{Code: ATECodeAlreadyExists, Err: err}
	default:
		return &ATEError{Code: ATECodeOther, Err: err}
	}
}

// ateStateFromProto projects the wire ActorState onto the seam vocabulary,
// preserving transitional states so MapATEState can fold them onto the
// client tri-state (Resuming->Active, Suspending/Reverting->Suspended,
// Pausing->Paused). Unknown future states pass through by name; MapATEState
// treats them as Active so Describe never fails on a valid server state.
func ateStateFromProto(state ateapipb.ActorState) ATEActorState {
	switch state {
	case ateapipb.ActorState_ACTOR_STATE_RUNNING:
		return ATEActorStateRunning
	case ateapipb.ActorState_ACTOR_STATE_RESUMING:
		return ATEActorStateResuming
	case ateapipb.ActorState_ACTOR_STATE_SUSPENDED:
		return ATEActorStateSuspended
	case ateapipb.ActorState_ACTOR_STATE_SUSPENDING:
		return ATEActorStateSuspending
	case ateapipb.ActorState_ACTOR_STATE_REVERTING:
		return ATEActorStateReverting
	case ateapipb.ActorState_ACTOR_STATE_CRASHED:
		return ATEActorStateCrashed
	case ateapipb.ActorState_ACTOR_STATE_PAUSED:
		return ATEActorStatePaused
	case ateapipb.ActorState_ACTOR_STATE_PAUSING:
		return ATEActorStatePausing
	case ateapipb.ActorState_ACTOR_STATE_DELETING:
		return ATEActorStateDeleting
	case ateapipb.ActorState_ACTOR_STATE_UNSPECIFIED:
		return ATEActorStateUnspecified
	default:
		return ATEActorState(state.String())
	}
}

// ateActorFromProto projects a wire Actor onto the seam type. Template and
// placement echoes let operators verify what a create requested.
func ateActorFromProto(pb *ateapipb.Actor) ATEActor {
	if pb == nil {
		return ATEActor{}
	}
	out := ATEActor{State: ATEActorStateUnspecified}
	if pb.GetMetadata() != nil {
		out.Atespace = pb.GetMetadata().GetAtespace()
		out.Name = pb.GetMetadata().GetName()
		out.UID = pb.GetMetadata().GetUid()
	}
	if pb.GetActorTemplate() != nil {
		out.TemplateAtespace = pb.GetActorTemplate().GetAtespace()
		out.TemplateName = pb.GetActorTemplate().GetName()
	}
	if pb.GetWorkerSelector() != nil {
		for key, value := range pb.GetWorkerSelector().GetMatchLabels() {
			if out.WorkerSelector == nil {
				out.WorkerSelector = map[string]string{}
			}
			out.WorkerSelector[key] = value
		}
	}
	if pb.GetStatus() != nil {
		out.State = ateStateFromProto(pb.GetStatus().GetState())
	}
	return out
}

// protoObjectRef builds the wire address for one actor.
func protoObjectRef(ref ATEObjectRef) *ateapipb.ObjectRef {
	return &ateapipb.ObjectRef{Atespace: ref.Atespace, Name: ref.Name}
}

// protoCreateActor builds the wire create payload from the seam spec:
// identity + template ref + per-actor worker selector.
func protoCreateActor(spec ATECreateSpec) *ateapipb.Actor {
	actor := &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: spec.Atespace, Name: spec.Name},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: spec.TemplateAtespace, Name: spec.TemplateName},
	}
	if len(spec.WorkerSelector) > 0 {
		actor.WorkerSelector = &ateapipb.Selector{MatchLabels: spec.WorkerSelector}
	}
	return actor
}

// grpcATEControl adapts the generated ControlClient onto the ATEControl seam.
type grpcATEControl struct {
	client ateapipb.ControlClient
}

func (g *grpcATEControl) GetActor(ctx context.Context, ref ATEObjectRef) (ATEActor, error) {
	pb, err := g.client.GetActor(ctx, &ateapipb.GetActorRequest{Actor: protoObjectRef(ref)})
	if err != nil {
		return ATEActor{}, mapGRPCError(err)
	}
	return ateActorFromProto(pb), nil
}

func (g *grpcATEControl) CreateActor(ctx context.Context, spec ATECreateSpec) (ATEActor, error) {
	pb, err := g.client.CreateActor(ctx, &ateapipb.CreateActorRequest{Actor: protoCreateActor(spec)})
	if err != nil {
		return ATEActor{}, mapGRPCError(err)
	}
	return ateActorFromProto(pb), nil
}

func (g *grpcATEControl) ResumeActor(ctx context.Context, ref ATEObjectRef) (ATEActor, bool, error) {
	resp, err := g.client.ResumeActor(ctx, &ateapipb.ResumeActorRequest{Actor: protoObjectRef(ref)})
	if err != nil {
		return ATEActor{}, false, mapGRPCError(err)
	}
	return ateActorFromProto(resp.GetActor()), resp.GetResumed(), nil
}

func (g *grpcATEControl) SuspendActor(ctx context.Context, ref ATEObjectRef) (ATEActor, error) {
	resp, err := g.client.SuspendActor(ctx, &ateapipb.SuspendActorRequest{Actor: protoObjectRef(ref)})
	if err != nil {
		return ATEActor{}, mapGRPCError(err)
	}
	return ateActorFromProto(resp.GetActor()), nil
}

func (g *grpcATEControl) PauseActor(ctx context.Context, ref ATEObjectRef) (ATEActor, error) {
	resp, err := g.client.PauseActor(ctx, &ateapipb.PauseActorRequest{Actor: protoObjectRef(ref)})
	if err != nil {
		return ATEActor{}, mapGRPCError(err)
	}
	return ateActorFromProto(resp.GetActor()), nil
}

// DialATEControl dials ateapi at cfg.Address and returns the ATEControl seam
// plus a close func. The caller owns the connection lifetime.
//
// Secure path (default): verified TLS via the live ClusterTrustBundle, then
// the bearer token (file > inline > minted ServiceAccount token), mirroring
// upstream ateclient.NewClient. Insecure path (cfg.Insecure, Kind-only):
// TLS with certificate verification skipped, restricted to loopback
// endpoints; bearer token attached like the secure path when available
// (file > inline > minted ServiceAccount token).
func DialATEControl(ctx context.Context, cfg ATEClientConfig, opts ATEDialOptions) (ATEControl, func(), error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	endpoint, err := normalizeATEEndpoint(cfg.Address)
	if err != nil {
		return nil, nil, err
	}
	if cfg.Insecure {
		if !isLoopbackEndpoint(endpoint) {
			return nil, nil, fmt.Errorf("substrate insecure-dev dial refuses non-loopback endpoint %q (set %s only for a local Kind port-forward)", endpoint, GateInsecureEnvVar)
		}
		// Kind-only skip-verify TLS: ateapi always serves TLS, so the
		// insecure escape hatch skips certificate verification on a TLS
		// channel instead of dialing plaintext. The loopback guard above is
		// what keeps this from ever reaching a shared cluster.
		dialOpts := []grpc.DialOption{
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
				InsecureSkipVerify: true, // #nosec G402 -- Kind-only loopback spike path; non-loopback endpoints are refused by the guard above.
				MinVersion:         tls.VersionTLS13,
				ServerName:         ateAPIServerName,
			})),
			grpc.WithDefaultServiceConfig(roundRobinServiceConfig),
		}
		if strings.TrimSpace(cfg.TokenFile) != "" {
			if _, err := loadTokenFromFile(strings.TrimSpace(cfg.TokenFile)); err != nil {
				return nil, nil, err
			}
			dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(fileBearerCreds{path: strings.TrimSpace(cfg.TokenFile), requireTLS: true}))
		} else if strings.TrimSpace(cfg.Token) != "" {
			dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(staticBearerCreds{token: strings.TrimSpace(cfg.Token), requireTLS: true}))
		} else if clientset, err := ateKubernetesClientset(opts); err == nil {
			// Best-effort mint like the secure path; without a kubeconfig or
			// in-cluster RBAC there is nothing to mint, so dial without a
			// token rather than failing the loopback spike.
			if tokenOpt, err := resolveBearerCredentials(ctx, cfg, clientset, true); err == nil {
				dialOpts = append(dialOpts, tokenOpt)
			}
		}
		conn, err := grpc.NewClient(endpoint, dialOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("dial ateapi at %s: %w", endpoint, err)
		}
		return &grpcATEControl{client: ateapipb.NewControlClient(conn)}, func() { _ = conn.Close() }, nil
	}
	clientset, err := ateKubernetesClientset(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("dial ateapi at %s: %w (or set %s for insecure-dev loopback only)", endpoint, err, GateInsecureEnvVar)
	}
	tlsCfg, err := serverTLSConfig(ctx, clientset)
	if err != nil {
		return nil, nil, fmt.Errorf("dial ateapi at %s: %w", endpoint, err)
	}
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithDefaultServiceConfig(roundRobinServiceConfig),
	}
	tokenOpt, err := resolveBearerCredentials(ctx, cfg, clientset, true)
	if err != nil {
		return nil, nil, fmt.Errorf("dial ateapi at %s: %w", endpoint, err)
	}
	dialOpts = append(dialOpts, tokenOpt)
	conn, err := grpc.NewClient(endpoint, dialOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("dial ateapi at %s: %w", endpoint, err)
	}
	return &grpcATEControl{client: ateapipb.NewControlClient(conn)}, func() { _ = conn.Close() }, nil
}

// DialATEClient dials ateapi and binds the full Client lifecycle onto it.
// It is the live entry point for the controller operator and the latency
// harness: cfg comes from the gate (endpoint, token file, atespace,
// template), opts carries kubeconfig selection.
func DialATEClient(ctx context.Context, cfg ATEClientConfig, opts ATEDialOptions) (Client, func(), error) {
	control, closeConn, err := DialATEControl(ctx, cfg, opts)
	if err != nil {
		return nil, nil, err
	}
	client, err := NewATEClient(cfg, control)
	if err != nil {
		closeConn()
		return nil, nil, err
	}
	return client, closeConn, nil
}
