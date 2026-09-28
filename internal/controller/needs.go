package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// ensureNeedSecrets creates the credentials for the app's database and
// cache the first time they are needed. They are never regenerated: the
// database is initialised with them.
func (r *AppReconciler) ensureNeedSecrets(ctx context.Context, app *v1alpha1.App, s spec.Spec) error {
	if s.Needs(spec.NeedPostgres) {
		user, db := "app", strings.ReplaceAll(app.Name, "-", "_")
		if err := r.ensureSecret(ctx, app, render.PostgresSecret, func(pw string) map[string]string {
			return map[string]string{
				"username": user, "password": pw, "database": db, "host": spec.NeedPostgres, "port": "5432",
				"uri": fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=disable", user, pw, spec.NeedPostgres, db),
			}
		}); err != nil {
			return err
		}
	}
	if s.Needs(spec.NeedRedis) {
		if err := r.ensureSecret(ctx, app, render.RedisSecret, func(pw string) map[string]string {
			return map[string]string{"password": pw, "host": spec.NeedRedis, "port": "6379",
				"uri": fmt.Sprintf("redis://:%s@%s:6379/0", pw, spec.NeedRedis)}
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *AppReconciler) ensureSecret(ctx context.Context, app *v1alpha1.App, name string, data func(password string) map[string]string) error {
	var existing corev1.Secret
	err := r.Get(ctx, client.ObjectKey{Namespace: app.Name, Name: name}, &existing)
	if err == nil || !apierrors.IsNotFound(err) {
		return err // present (kept as it is) or a real error
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: app.Name, Labels: map[string]string{
			render.LabelManagedBy: render.ManagedBy, render.LabelApp: app.Name,
		}},
		StringData: data(hex.EncodeToString(b)),
	}
	if err := controllerutil.SetControllerReference(app, sec, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, sec); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// serviceURLs looks up the services the app's services need and returns
// the address to inject for each: its app port (not the port-80 alias),
// by short name within the app's namespace, by cluster DNS name otherwise.
func (r *AppReconciler) serviceURLs(ctx context.Context, app *v1alpha1.App, s spec.Spec) (map[string]string, error) {
	out := map[string]string{}
	for _, ref := range render.NeedRefs(s) {
		ns, name, found := strings.Cut(ref, "/")
		if !found {
			ns, name = app.Name, ref
		}
		if !found && isOwnService(s, name) {
			// A service of this app: its port is known without a lookup, and
			// it may not have been created yet.
			for _, svc := range s.Services {
				if svc.Name == name {
					out[ref] = address(name, int32(svc.Port))
				}
			}
			continue
		}
		var svc corev1.Service
		if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &svc); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("needs %s, but there is no such service", ref)
			}
			return nil, err
		}
		if len(svc.Spec.Ports) == 0 {
			return nil, fmt.Errorf("needs %s, which exposes no port", ref)
		}
		port := svc.Spec.Ports[0].Port
		for _, p := range svc.Spec.Ports {
			if p.Name == "app" {
				port = p.Port
			}
		}
		host := name
		if ns != app.Name {
			host = name + "." + ns + ".svc.cluster.local"
		}
		out[ref] = address(host, port)
	}
	return out, nil
}

func isOwnService(s spec.Spec, name string) bool {
	for _, svc := range s.Services {
		if svc.Name == name {
			return true
		}
	}
	return false
}

func address(host string, port int32) string {
	switch port {
	case 80:
		return "http://" + host
	case 443:
		return "https://" + host
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}
