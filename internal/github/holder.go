package github

import (
	"context"
	"errors"
	"io/fs"
	"sync/atomic"
)

// ErrNotConfigured is returned until the GitHub App has been created in the setup flow.
var ErrNotConfigured = errors.New("GitHub App is not configured yet; finish setup first")

// Holder holds the current GitHub App, which appears at runtime after the
// one-click setup, and adapts it to the per-installation calls the platform makes.
type Holder struct{ app atomic.Pointer[App] }

func (h *Holder) Set(a *App) { h.app.Store(a) }

func (h *Holder) Get() (*App, error) {
	if a := h.app.Load(); a != nil {
		return a, nil
	}
	return nil, ErrNotConfigured
}

func (h *Holder) client(installation int64) (*Client, error) {
	a, err := h.Get()
	if err != nil {
		return nil, err
	}
	return a.Client(installation), nil
}

func (h *Holder) FileAt(ctx context.Context, installation int64, repo, path, ref string) ([]byte, error) {
	c, err := h.client(installation)
	if err != nil {
		return nil, err
	}
	return c.FileAt(ctx, repo, path, ref)
}

func (h *Holder) RepoFS(ctx context.Context, installation int64, repo, ref string) (fs.FS, error) {
	c, err := h.client(installation)
	if err != nil {
		return nil, err
	}
	return c.FS(ctx, repo, ref)
}

func (h *Holder) OpenPR(ctx context.Context, installation int64, repo, base, branch, title, body string, files map[string]string) (*PullRequest, error) {
	c, err := h.client(installation)
	if err != nil {
		return nil, err
	}
	return c.OpenPR(ctx, repo, base, branch, title, body, files)
}

func (h *Holder) CreateCheck(ctx context.Context, installation int64, repo string, cr CheckRun) (int64, error) {
	c, err := h.client(installation)
	if err != nil {
		return 0, err
	}
	return c.CreateCheckRun(ctx, repo, cr)
}

func (h *Holder) UpdateCheck(ctx context.Context, installation int64, repo string, id int64, cr CheckRun) error {
	c, err := h.client(installation)
	if err != nil {
		return err
	}
	return c.UpdateCheckRun(ctx, repo, id, cr)
}

func (h *Holder) CloneToken(ctx context.Context, installation int64) (string, error) {
	a, err := h.Get()
	if err != nil {
		return "", err
	}
	return a.InstallationToken(ctx, installation)
}

func (h *Holder) ChangedFiles(ctx context.Context, installation int64, repo, base, head string) ([]string, bool, error) {
	c, err := h.client(installation)
	if err != nil {
		return nil, false, err
	}
	return c.ChangedFiles(ctx, repo, base, head)
}

func (h *Holder) BranchSHA(ctx context.Context, installation int64, repo, branch string) (string, error) {
	c, err := h.client(installation)
	if err != nil {
		return "", err
	}
	return c.BranchSHA(ctx, repo, branch)
}
