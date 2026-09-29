package api

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gh "github.com/p0dxD/rendimiento.ai/internal/github"
)

// SecretCredentials stores the GitHub App credentials in a Kubernetes Secret.
type SecretCredentials struct {
	Client    client.Client
	Namespace string
	Name      string // e.g. rendimiento-github
}

const credentialsKey = "credentials.json"

// Load returns the GitHub App's credentials, or nil before setup has run.
func (s *SecretCredentials) Load(ctx context.Context) (*gh.Credentials, error) {
	var sec corev1.Secret
	err := s.Client.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: s.Name}, &sec)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c gh.Credentials
	if err := json.Unmarshal(sec.Data[credentialsKey], &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save stores the GitHub App's credentials (created or replaced).
func (s *SecretCredentials) Save(ctx context.Context, c *gh.Credentials) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: s.Namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "rendimiento"}},
		Data:       map[string][]byte{credentialsKey: raw},
	}
	err = s.Client.Create(ctx, sec)
	if apierrors.IsAlreadyExists(err) {
		// Never silently replace an existing app's credentials.
		return err
	}
	return err
}
