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
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/addon"
	"github.com/p0dxD/rendimiento.ai/internal/analytics"
	"github.com/p0dxD/rendimiento.ai/internal/api"
	"github.com/p0dxD/rendimiento.ai/internal/catalog"
	"github.com/p0dxD/rendimiento.ai/internal/controller"
	"github.com/p0dxD/rendimiento.ai/internal/dns"
	"github.com/p0dxD/rendimiento.ai/internal/environment"
	"github.com/p0dxD/rendimiento.ai/internal/events"
	"github.com/p0dxD/rendimiento.ai/internal/generate"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/i18n"
	"github.com/p0dxD/rendimiento.ai/internal/logarchive"
	"github.com/p0dxD/rendimiento.ai/internal/notify"
	"github.com/p0dxD/rendimiento.ai/internal/pipeline"
	"github.com/p0dxD/rendimiento.ai/internal/platform"
	"github.com/p0dxD/rendimiento.ai/internal/problems"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/renovate"
	"github.com/p0dxD/rendimiento.ai/internal/ruta"
	"github.com/p0dxD/rendimiento.ai/internal/store"
	"github.com/p0dxD/rendimiento.ai/internal/uptime"
	"github.com/p0dxD/rendimiento.ai/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	// Warnings and errors also go to the Problems page; plain is the same
	// log without that, for the recorder's own messages.
	plain := slog.NewJSONHandler(os.Stdout, nil)
	problemLog := problems.NewRecorder()
	log := slog.New(problemLog.Handler(plain))
	ctrl.SetLogger(toLogr(log))
	if err := run(log, problemLog, slog.New(plain)); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, problemLog *problems.Recorder, plain *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --8<-- [start:startup]
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
	go problemLog.Run(ctx, st, plain)
	if requeued, failed, err := st.RequeueOrphans(ctx); err != nil {
		return fmt.Errorf("recover interrupted runs: %w", err)
	} else if requeued+failed > 0 {
		log.Warn("recovered runs interrupted by the last restart", "requeued", requeued, "failed", failed)
	}

	// --8<-- [end:startup]

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
	// VOLUME_LABELS is KEY=VALUE pairs added to every app volume claim, e.g.
	// the Longhorn labels that put it in the nightly backup job.
	renderOpts.VolumeLabels = map[string]string{}
	for _, kv := range splitList(env("VOLUME_LABELS", "")) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fmt.Errorf("VOLUME_LABELS must be KEY=VALUE pairs, got %q", kv)
		}
		renderOpts.VolumeLabels[k] = v
	}
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

	// Add-ons act as their own, more privileged service account, so what
	// they change is attributed to it; the platform may only impersonate it.
	addonCfg := rest.CopyConfig(cfg)
	addonCfg.Impersonate = rest.ImpersonationConfig{UserName: env("ADDONS_IDENTITY", "system:serviceaccount:"+ns+":rendimiento-addons")}
	addonTarget, err := client.New(addonCfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	// Helm renders against this cluster's version and APIs; cache discovery
	// and refresh it periodically so newly installed CRDs show up.
	disco := memory.NewMemCacheClient(discovery.NewDiscoveryClientForConfigOrDie(cfg))
	go func() {
		for range time.Tick(10 * time.Minute) {
			disco.Invalidate()
		}
	}()
	addonSyncer := &addon.Syncer{Kube: kube, GitHub: holder, Repo: env("ADDONS_REPO", "p0dxD/gitops"), Log: log}
	if err := (&controller.AddonReconciler{
		Client: mgr.GetClient(), Target: addonTarget,
		Renderer: &addon.Renderer{Git: addon.GitHubFetcher{GitHub: holder}, Discovery: disco},
	}).SetupWithManager(mgr); err != nil {
		return err
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
		Client:            clientset,
		Namespace:         env("BUILD_NAMESPACE", "rendimiento-builds"),
		BuildkitAddr:      env("BUILDKIT_ADDR", "tcp://buildkitd.devops-tools.svc.cluster.local:1234"),
		BuildkitPool:      os.Getenv("BUILDKIT_POOL"),
		BuildImage:        env("BUILDKIT_IMAGE", "moby/buildkit:v0.18.2"),
		InsecureRegistry:  env("REGISTRY_INSECURE", "true") == "true",
		Token:             p.CloneToken,
		Timeout:           stepTimeout,
		ExcludeNodes:      splitList(os.Getenv("BUILD_EXCLUDE_NODES")),
		RailpackImage:     os.Getenv("RAILPACK_IMAGE"),
		RailpackFrontend:  os.Getenv("RAILPACK_FRONTEND"),
		PostgresImage:     os.Getenv("TEST_POSTGRES_IMAGE"),
		CacheStorageClass: os.Getenv("TEST_CACHE_STORAGE_CLASS"),
		CacheSize:         os.Getenv("TEST_CACHE_SIZE"),
	}
	p.Runner = pipeline.NewRunner(executor, p, parallel)
	p.Caches = executor
	renovateRunner := &renovate.Runner{
		Kube: clientset, Namespace: executor.Namespace, ExcludeNodes: executor.ExcludeNodes,
		Store: st, GitHub: holder, Log: log,
	}

	srv := &api.Server{
		BookURL: os.Getenv("BOOK_URL"), BookURLEs: os.Getenv("BOOK_URL_ES"),
		Platform: p, Store: st, Kube: kube, GitHub: holder, Credentials: creds, DNS: dnsProvider, Log: log,
		BaseURL: baseURL, AllowedUsers: users, SetupToken: os.Getenv("SETUP_TOKEN"),
		AppName: env("GITHUB_APP_NAME", "rendimiento"), UI: web.Dist(),
		Catalog:  &catalog.Builder{Kube: kube},
		Renovate: renovateRunner,
		Addons:   addonSyncer,
		MCP:      (&ruta.Server{Store: st, Log: log.With("component", "ruta")}).Handler(),
		Environment: &environment.Checker{
			Kube: kube, Discovery: clientset.Discovery(), DNS: dnsProvider, GitHub: holder, DB: st,
			Config: environment.Config{
				IngressClass: renderOpts.IngressClass, ClusterIssuer: renderOpts.ClusterIssuer, StorageClass: renderOpts.StorageClass,
				BuildkitAddr: executor.BuildkitAddr, BuildkitPool: executor.BuildkitPool, Registry: p.Config.Registry, RegistryInsecure: executor.InsecureRegistry,
				BuildNamespace: executor.Namespace, PlatformURL: baseURL, ExcludeNodes: executor.ExcludeNodes,
			},
		},
	}
	// Uptime checks of every app's services (the Reliability tab), on the
	// leader only, so one replica checks.
	var prober *uptime.Prober
	checker := uptime.NewChecker(10 * time.Second)
	if every, err := time.ParseDuration(env("UPTIME_INTERVAL", "1m")); err != nil || (every != 0 && every < 10*time.Second) {
		return fmt.Errorf("UPTIME_INTERVAL must be 0 (off) or a duration of at least 10s, got %q", os.Getenv("UPTIME_INTERVAL"))
	} else if every > 0 {
		prober = &uptime.Prober{Store: st, Checker: checker, Interval: every, Log: log.With("component", "uptime")}
		uptime.Register(ctrlmetrics.Registry)
	}
	// Email notifications: failed runs, rollbacks, outages, recoveries.
	notifier := &notify.Notifier{To: splitList(os.Getenv("NOTIFY_EMAIL_TO")), Log: log.With("component", "notify"), Lang: i18n.Parse(env("NOTIFY_LANG", "en"))}
	if key := os.Getenv("RESEND_API_KEY"); key != "" {
		notifier.Sender = &notify.Resend{APIKey: key, From: env("NOTIFY_EMAIL_FROM", "rendimiento <alerts@joserod.space>")}
	}
	notifier.OnSent = func() {
		// A sent email shows earlier send failures are over.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := st.ResolveProblems(ctx, store.ProblemMatch{Message: notify.MsgSendFailed}, "an email was sent since"); err != nil {
			plain.Warn("could not resolve problems", "err", err)
		}
	}
	p.Notify = notifier
	if prober != nil {
		prober.Notify, prober.BaseURL = notifier, baseURL
	}
	srv.Notify = notifier
	srv.PublicStats = env("PUBLIC_STATS", "false") == "true"
	srv.PublicActivity = env("PUBLIC_ACTIVITY", "false") == "true"
	srv.PublicOrigins = splitList(os.Getenv("PUBLIC_STATS_ORIGINS"))
	srv.PublicTimeZone = env("PUBLIC_STATS_TZ", "UTC")
	// Visitor numbers from Umami, read with a view-only Umami user.
	if umamiURL := os.Getenv("UMAMI_URL"); umamiURL != "" {
		if os.Getenv("UMAMI_USERNAME") == "" || os.Getenv("UMAMI_PASSWORD") == "" {
			log.Warn("UMAMI_URL is set but UMAMI_USERNAME or UMAMI_PASSWORD is not: visitor numbers are off")
		} else {
			srv.Analytics = &analytics.Umami{URL: umamiURL, Username: os.Getenv("UMAMI_USERNAME"), Password: os.Getenv("UMAMI_PASSWORD")}
			srv.AnalyticsURL = strings.TrimRight(os.Getenv("UMAMI_PUBLIC_URL"), "/")
		}
	}
	if u, ok := srv.Analytics.(*analytics.Umami); ok {
		srv.Environment.Config.Analytics = func(ctx context.Context) (bool, string) {
			sites, err := u.Websites(ctx)
			if errors.Is(err, analytics.ErrAuth) {
				return false, i18n.M("Umami refused rendimiento's user name or password (UMAMI_USERNAME, UMAMI_PASSWORD)")
			} else if err != nil {
				return false, i18n.M("cannot reach Umami: %s", err.Error())
			}
			return true, i18n.M("rendimiento can read %d Umami websites; each app's Visits tab shows the ones whose domain is the app's", len(sites))
		}
	}
	// With STATS_LISTEN, the stats are served only on that internal port
	// (not routed by the public ingress), never on the public listener.
	statsAddr := os.Getenv("STATS_LISTEN")
	srv.StatsInternalOnly = statsAddr != ""
	srv.Environment.Config.NotifyTo, srv.Environment.Config.NotifyReady = notifier.To, notifier.Enabled()
	log.Info("email notifications", "enabled", notifier.Enabled(), "to", notifier.To)

	// Log archive: finished runs' step logs move to S3-compatible storage (Garage here),
	// which deletes them after LOG_RETENTION_DAYS.
	var logArchive *logarchive.Archive
	var logBucket *logarchive.MinIO
	if endpoint := os.Getenv("LOG_ARCHIVE_ENDPOINT"); endpoint != "" {
		days, err := strconv.Atoi(env("LOG_RETENTION_DAYS", "365"))
		if err != nil || days < 1 {
			return fmt.Errorf("LOG_RETENTION_DAYS must be a number of days, got %q", os.Getenv("LOG_RETENTION_DAYS"))
		}
		logBucket, err = logarchive.NewMinIO(endpoint, os.Getenv("LOG_ARCHIVE_ACCESS_KEY"), os.Getenv("LOG_ARCHIVE_SECRET_KEY"),
			env("LOG_ARCHIVE_BUCKET", "rendimiento-logs"), env("LOG_ARCHIVE_SECURE", "false") == "true")
		if err != nil {
			return fmt.Errorf("log archive: %w", err)
		}
		logArchive = &logarchive.Archive{Objects: logBucket, Store: st, Log: log.With("component", "logarchive"), RetentionDays: days}
		srv.Logs = logArchive
		srv.Environment.Config.LogArchive = func(ctx context.Context) (bool, string) {
			if ok, err := logBucket.Client.BucketExists(ctx, logBucket.Bucket); err != nil || !ok {
				if err == nil {
					err = fmt.Errorf("bucket %q does not exist", logBucket.Bucket)
				}
				return false, i18n.M("cannot reach the archive: %s", err.Error())
			}
			n, size, pending, err := st.ArchiveStats(ctx)
			if err != nil {
				return false, err.Error()
			}
			return true, i18n.M("%d step logs archived (%.1f MB of text) in %s/%s, %d waiting; kept %d days", n, float64(size)/1e6, endpoint, logBucket.Bucket, pending, days)
		}
	}

	// Release verification: watch each new release, roll back a broken one.
	verifyWindow, err := time.ParseDuration(env("VERIFY_WINDOW", "5m"))
	if err != nil || (verifyWindow != 0 && verifyWindow < time.Minute) {
		return fmt.Errorf("VERIFY_WINDOW must be 0 (off) or a duration of at least 1m, got %q", os.Getenv("VERIFY_WINDOW"))
	}
	p.Verify = platform.VerifySettings{Window: verifyWindow, Check: checker.Check}
	p.Clientset = clientset

	httpSrv := &http.Server{Addr: env("LISTEN", ":8080"), Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 4)
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
			go addonSyncer.Loop(ctx)
			if prober != nil {
				go prober.Run(ctx)
			}
			p.ResumeVerifications(ctx)
			if logArchive != nil {
				go func() {
					if err := logBucket.EnsureRetention(ctx, logArchive.RetentionDays); err != nil {
						log.Warn("log archive: could not set the retention rule", "err", err)
					}
					logArchive.Run(ctx)
				}()
			}
			p.Work(ctx)
		case <-ctx.Done():
		}
		errc <- nil
	}()
	var statsSrv *http.Server
	if statsAddr != "" {
		statsSrv = &http.Server{Addr: statsAddr, Handler: srv.StatsHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			log.Info("serving public stats internally", "addr", statsAddr)
			if err := statsSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}
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
	if statsSrv != nil {
		_ = statsSrv.Shutdown(shutdown)
	}
	return httpSrv.Shutdown(shutdown)
}

func ptr[T any](v T) *T { return &v }

// splitList splits a comma- or space-separated setting.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}
