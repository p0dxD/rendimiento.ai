package controller

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/testenv"
)

// The package's tests share one envtest: starting an API server and etcd
// for each of them made the package take about 250 s on the cluster's
// workers, and several runs doing it at once timed etcd out. Each test still
// starts its own manager; when it ends, it deletes its Apps or Addons while
// that manager runs (so their finalizers finish). Namespaces cannot be
// deleted in envtest (they stay Terminating), so every test uses its own
// app and namespace names.
var (
	sharedEnv     *envtest.Environment
	sharedCfg     *rest.Config
	sharedEnvErr  error
	sharedEnvOnce sync.Once
)

// kubeConfig starts the shared envtest the first time a test needs it, or
// skips (fails, when KUBEBUILDER_ASSETS is set) if it cannot start.
func kubeConfig(t *testing.T) *rest.Config {
	t.Helper()
	sharedEnvOnce.Do(func() {
		sharedEnv = testenv.New(filepath.Join("..", "..", "deploy", "crds"), filepath.Join("testdata", "crds"))
		sharedCfg, sharedEnvErr = sharedEnv.Start()
	})
	if sharedEnvErr != nil {
		if os.Getenv("KUBEBUILDER_ASSETS") != "" {
			// Configured but failed to start (e.g. an overloaded machine):
			// fail, so a test gate never passes with these tests skipped.
			t.Fatalf("envtest failed to start: %v", sharedEnvErr)
		}
		t.Skipf("envtest unavailable (run setup-envtest and set KUBEBUILDER_ASSETS): %v", sharedEnvErr)
	}
	return sharedCfg
}

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedEnv != nil && sharedCfg != nil {
		_ = sharedEnv.Stop()
	}
	os.Exit(code)
}

// deleteAll deletes every object of a kind and waits for its controller to
// finish their finalizers; if it does not within 30 seconds, it drops the
// finalizers, so the next test starts without them either way.
func deleteAll(c client.Client, list client.ObjectList, items func() []client.Object) {
	ctx := context.Background()
	if c.List(ctx, list) != nil {
		return
	}
	for _, o := range items() {
		_ = c.Delete(ctx, o)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if c.List(ctx, list) == nil && len(items()) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, o := range items() {
		o.SetFinalizers(nil)
		_ = c.Update(ctx, o)
	}
}

func deleteApps(c client.Client) {
	var list v1alpha1.AppList
	deleteAll(c, &list, func() []client.Object {
		out := make([]client.Object, len(list.Items))
		for i := range list.Items {
			out[i] = &list.Items[i]
		}
		return out
	})
}

func deleteAddons(c client.Client) {
	var list v1alpha1.AddonList
	deleteAll(c, &list, func() []client.Object {
		out := make([]client.Object, len(list.Items))
		for i := range list.Items {
			out[i] = &list.Items[i]
		}
		return out
	})
}
