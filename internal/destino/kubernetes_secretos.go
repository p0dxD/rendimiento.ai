package destino

import (
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rendimiento-ai/rendimiento/internal/render"
)

// secreto is the Secret named in the app's namespace, once the namespace
// exists and is the app's (ErrSinLugar otherwise).
func (k Kubernetes) secreto(ctx context.Context, app, nombre string) (*corev1.Secret, error) {
	var ns corev1.Namespace
	if err := k.Kube.Get(ctx, client.ObjectKey{Name: app}, &ns); err != nil || ns.Labels[render.LabelApp] != app {
		return nil, ErrSinLugar
	}
	return &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: nombre, Namespace: app, Labels: map[string]string{render.LabelApp: app, render.LabelManagedBy: render.ManagedBy}},
	}, nil
}

// LlavesSecreto lists the keys of the Secret in the app's namespace.
func (k Kubernetes) LlavesSecreto(ctx context.Context, app, nombre string) ([]string, error) {
	sec, err := k.secreto(ctx, app, nombre)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	err = k.Kube.Get(ctx, client.ObjectKeyFromObject(sec), sec)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	for key := range sec.Data {
		keys = append(keys, key)
	}
	for key := range sec.StringData {
		if _, dup := sec.Data[key]; !dup {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys, nil
}

// GuardarSecreto writes the values straight into the Secret in the app's
// namespace; they are never stored in git or in Postgres.
func (k Kubernetes) GuardarSecreto(ctx context.Context, app, nombre string, valores map[string]*string) error {
	sec, err := k.secreto(ctx, app, nombre)
	if err != nil {
		return err
	}
	var existing corev1.Secret
	err = k.Kube.Get(ctx, client.ObjectKeyFromObject(sec), &existing)
	switch {
	case apierrors.IsNotFound(err):
		sec.StringData = map[string]string{}
		for key, v := range valores {
			if v != nil {
				sec.StringData[key] = *v
			}
		}
		return k.Kube.Create(ctx, sec)
	case err != nil:
		return err
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	// The API server folds stringData into data on write; a fake client
	// does not, so fold it here too.
	for key, v := range existing.StringData {
		existing.Data[key] = []byte(v)
	}
	existing.StringData = nil
	for key, v := range valores {
		if v == nil {
			delete(existing.Data, key)
		} else {
			existing.Data[key] = []byte(*v)
		}
	}
	return k.Kube.Update(ctx, &existing)
}
