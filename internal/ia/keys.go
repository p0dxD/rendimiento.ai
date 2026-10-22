package ia

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretKeys keeps each organization's own AI key in a Secret of the
// platform's namespace (rendimiento-ia-org-<id>), never in the database;
// the UI only ever sees its last four characters.
type SecretKeys struct {
	Kube      client.Client
	Namespace string
}

const keyField = "api-key"

func secretName(orgID int64) string { return fmt.Sprintf("rendimiento-ia-org-%d", orgID) }

// OrgKey is the organization's key ("" when it has none).
func (k SecretKeys) OrgKey(ctx context.Context, orgID int64) (string, error) {
	var s corev1.Secret
	err := k.Kube.Get(ctx, client.ObjectKey{Namespace: k.Namespace, Name: secretName(orgID)}, &s)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(s.Data[keyField]), nil
}

// KeyHint is the key's last four characters, for the UI ("" when none).
func (k SecretKeys) KeyHint(ctx context.Context, orgID int64) (string, error) {
	key, err := k.OrgKey(ctx, orgID)
	if err != nil || len(key) < 8 {
		return "", err
	}
	return "…" + key[len(key)-4:], nil
}

// SetOrgKey stores the organization's key; an empty one removes it.
func (k SecretKeys) SetOrgKey(ctx context.Context, orgID int64, key string) error {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: k.Namespace, Name: secretName(orgID),
		Labels: map[string]string{"app.kubernetes.io/managed-by": "rendimiento", "rendimiento.ai/purpose": "ai-key"}}}
	if key == "" {
		if err := k.Kube.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	s.Data = map[string][]byte{keyField: []byte(key)}
	if err := k.Kube.Create(ctx, s); apierrors.IsAlreadyExists(err) {
		return k.Kube.Update(ctx, s)
	} else {
		return err
	}
}
