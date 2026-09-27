// Command rendimiento runs the whole platform in one process: API and UI,
// CI workers and the App controller.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/api"
	"github.com/p0dxD/rendimiento.ai/internal/catalog"
	"github.com/p0dxD/rendimiento.ai/internal/controller"
	"github.com/p0dxD/rendimiento.ai/internal/dns"
	"github.com/p0dxD/rendimiento.ai/internal/environment"
	"github.com/p0dxD/rendimiento.ai/internal/events"
	"github.com/p0dxD/rendimiento.ai/internal/generate"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
	"github.com/p0dxD/rendimiento.ai/internal/platform"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/renovate"
	"github.com/p0dxD/rendimiento.ai/internal/store"
	"github.com/p0dxD/rendimiento.ai/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctrl.SetLogger(toLogr(log))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	baseURL := strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/")
	ns := env("NAMESPACE", "rendimiento-system")
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	users := strings.FieldsFunc(os.Getenv("ALLOWED_USERS"), func(r rune) bool { return r == ',' || r == ' ' })
	if len(users) == 0 {
		return errors.New("ALLOWED_USERS is required (comma-separated GitHub logins)")
	}

	st, err := store.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if requeued, failed, err := st.RequeueOrphans(ctx); err != nil {
		return fmt.Errorf("recover interrupted runs: %w", err)
	} else if requeued+failed > 0 {
		log.Warn("recovered runs interrupted by the last restart", "requeued", requeued, "failed", failed)
	}

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)
	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: env("METRICS_ADDR", ":9090")},
		HealthProbeBindAddress:  "0",
		LeaderElection:          env("LEADER_ELECTION", "true") == "true",
		LeaderElectionID:        "rendimiento-controller",
		LeaderElectionNamespace: ns,
		// Pi control planes stall under build load; the 10s default renew
		// deadline made the process exit mid-build.
		LeaseDuration: ptr(60 * time.Second),
		RenewDeadline: ptr(45 * time.Second),
		RetryPeriod:   ptr(10 * time.Second),
	})
	if err != nil {
		return err
	}
	kube, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	var dnsProvider dns.Provider = dns.Noop{}
	if tok := os.Getenv("CLOUDFLARE_API_TOKEN"); tok != "" {
		target := os.Getenv("DNS_TARGET")
		if target == "" {
			return errors.New("DNS_TARGET (public IP, hostname, or \"auto\" for dynamic DNS) is required with CLOUDFLARE_API_TOKEN")
		}
		cf := &dns.Cloudflare{Token: tok, Target: target, Proxied: env("DNS_PROXIED", "false") == "true"}
		dnsProvider = cf
		if strings.EqualFold(target, "auto") {
			every, err := time.ParseDuration(env("DDNS_INTERVAL", "5m"))
			if err != nil || every < time.Minute {
				return fmt.Errorf("DDNS_INTERVAL must be a duration of at least 1m, got %q", os.Getenv("DDNS_INTERVAL"))
			}
			log.Info("dynamic DNS enabled: records follow this network's public IP", "interval", every)
			go cf.Run(ctx, every, log.Info)
		}
	} else {
		log.Warn("CLOUDFLARE_API_TOKEN not set: DNS records must be created by hand")
	}

	renderOpts := render.DefaultOptions()
	renderOpts.ClusterIssuer = env("CLUSTER_ISSUER", renderOpts.ClusterIssuer)
	renderOpts.IngressClass = env("INGRESS_CLASS", renderOpts.IngressClass)
	renderOpts.StorageClass = env("STORAGE_CLASS", renderOpts.StorageClass)
	renderOpts.GPU = render.GPUProfile{
		Resource:     env("GPU_RESOURCE", ""),
		RuntimeClass: env("GPU_RUNTIME_CLASS", ""),
		HostPaths:    splitList(env("GPU_HOST_PATHS", "")),
		SharedMemory: env("GPU_SHARED_MEMORY", ""),
		Env:          map[string]string{},
	}
	// GPU_ENV is KEY=VALUE pairs separated by ";" (values may contain ":" and ",").
	for _, kv := range strings.Split(env("GPU_ENV", ""), ";") {
		if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok && k != "" {
			renderOpts.GPU.Env[k] = v
		}
	}
	if err := (&controller.AppReconciler{Client: mgr.GetClient(), Scheme: scheme, Render: renderOpts, DNS: dnsProvider}).SetupWithManager(mgr); err != nil {
		return err
	}

	holder := &gh.Holder{}
	creds := &api.SecretCredentials{Client: kube, Namespace: ns, Name: env("GITHUB_SECRET", "rendimiento-github")}
	if c, err := creds.Load(ctx); err != nil {
		return fmt.Errorf("load GitHub credentials: %w", err)
	} else if c != nil {
		app, err := gh.New(*c)
		if err != nil {
			return err
		}
		holder.Set(app)
		log.Info("GitHub App loaded", "slug", c.Slug)
	} else {
		log.Warn("GitHub App not configured yet; open " + baseURL + "/setup")
	}

	parallel, _ := strconv.Atoi(env("MAX_PARALLEL_STEPS", "2"))
	stepTimeout, err := time.ParseDuration(env("STEP_TIMEOUT", "45m"))
	if err != nil || stepTimeout < time.Minute {
		return fmt.Errorf("STEP_TIMEOUT must be a duration of at least 1m, got %q", os.Getenv("STEP_TIMEOUT"))
	}
	p := &platform.Platform{
		Store: st, Kube: kube, GitHub: holder, Generator: generate.Templates{}, Hub: events.NewHub(), Log: log,
		Config: platform.Config{Registry: env("REGISTRY", "registry.example.lan:5000"), BaseURL: baseURL, Zone: os.Getenv("DNS_ZONE"), Railpack: os.Getenv("RAILPACK_IMAGE") != ""},
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	executor := &pipeline.KubeExecutor{
		Client:           clientset,
		Namespace:        env("BUILD_NAMESPACE", "rendimiento-builds"),
		BuildkitAddr:     env("BUILDKIT_ADDR", "tcp://buildkitd.devops-tools.svc.cluster.local:1234"),
		BuildImage:       env("BUILDKIT_IMAGE", "moby/buildkit:v0.18.2"),
		InsecureRegistry: env("REGISTRY_INSECURE", "true") == "true",
		Token:            p.CloneToken,
		Timeout:          stepTimeout,
		ExcludeNodes:     splitList(os.Getenv("BUILD_EXCLUDE_NODES")),
		RailpackImage:    os.Getenv("RAILPACK_IMAGE"),
		RailpackFrontend: os.Getenv("RAILPACK_FRONTEND"),
	}
	p.Runner = pipeline.NewRunner(executor, p, parallel)
	renovateRunner := &renovate.Runner{
		Kube: clientset, Namespace: executor.Namespace, ExcludeNodes: executor.ExcludeNodes,
		Store: st, GitHub: holder, Log: log,
	}

	srv := &api.Server{
		Platform: p, Store: st, Kube: kube, GitHub: holder, Credentials: creds, DNS: dnsProvider, Log: log,
		BaseURL: baseURL, AllowedUsers: users, SetupToken: os.Getenv("SETUP_TOKEN"),
		AppName: env("GITHUB_APP_NAME", "rendimiento"), UI: web.Dist(),
		Catalog:  &catalog.Builder{Kube: kube},
		Renovate: renovateRunner,
		Environment: &environment.Checker{
			Kube: kube, Discovery: clientset.Discovery(), DNS: dnsProvider, GitHub: holder, DB: st,
			Config: environment.Config{
				IngressClass: renderOpts.IngressClass, ClusterIssuer: renderOpts.ClusterIssuer, StorageClass: renderOpts.StorageClass,
				BuildkitAddr: executor.BuildkitAddr, Registry: p.Config.Registry, RegistryInsecure: executor.InsecureRegistry,
				BuildNamespace: executor.Namespace, PlatformURL: baseURL, ExcludeNodes: executor.ExcludeNodes,
			},
		},
	}
	httpSrv := &http.Server{Addr: env("LISTEN", ":8080"), Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 3)
	go func() { errc <- mgr.Start(ctx) }()
	go func() {
		// CI workers run only on the leader so one replica owns the queue.
		select {
		case <-mgr.Elected():
			if n, err := executor.Cleanup(ctx); err != nil {
				log.Warn("could not clean up old build pods", "err", err)
			} else if n > 0 {
				log.Info("removed build pods left by the previous process", "count", n)
			}
			log.Info("starting CI worker", "maxParallelSteps", parallel)
			go renovateRunner.Loop(ctx)
			p.Work(ctx)
		case <-ctx.Done():
		}
		errc <- nil
	}()
	go func() {
		log.Info("listening", "addr", httpSrv.Addr, "baseURL", baseURL)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil {
			stop()
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdown)
}

func ptr[T any](v T) *T { return &v }

// splitList splits a comma- or space-separated setting.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}
