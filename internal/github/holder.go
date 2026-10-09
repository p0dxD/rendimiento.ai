package github

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync/atomic"
)

// ErrNotConfigured is returned until the GitHub App has been created in the setup flow.
var ErrNotConfigured = errors.New("GitHub App is not configured yet; finish setup first")

// Holder holds the current GitHub App, which appears at runtime after the
// one-click setup, and adapts it to the per-installation calls the platform makes.
type Holder struct {
	app atomic.Pointer[App]
	// Accounts limits every App it holds (see App.SetAccounts).
	Accounts []string
}

// Set installs the App (at startup, or after the setup flow).
func (h *Holder) Set(a *App) {
	if len(h.Accounts) > 0 {
		a.SetAccounts(h.Accounts)
	}
	h.app.Store(a)
}

// Get returns the App, or ErrNotConfigured before setup.
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

// FileAt reads one file of repo at ref.
func (h *Holder) FileAt(ctx context.Context, installation int64, repo, path, ref string) ([]byte, error) {
	c, err := h.client(installation)
	if err != nil {
		return nil, err
	}
	return c.FileAt(ctx, repo, path, ref)
}

// RepoFS returns repo's tree at ref as an fs.FS.
func (h *Holder) RepoFS(ctx context.Context, installation int64, repo, ref string) (fs.FS, error) {
	c, err := h.client(installation)
	if err != nil {
		return nil, err
	}
	return c.FS(ctx, repo, ref)
}

// OpenPR commits files to branch and opens (or finds) a pull request into base.
func (h *Holder) OpenPR(ctx context.Context, installation int64, repo, base, branch, title, body string, files map[string]string) (*PullRequest, error) {
	c, err := h.client(installation)
	if err != nil {
		return nil, err
	}
	return c.OpenPR(ctx, repo, base, branch, title, body, files)
}

// CreateCheck starts a check run on a commit.
func (h *Holder) CreateCheck(ctx context.Context, installation int64, repo string, cr CheckRun) (int64, error) {
	c, err := h.client(installation)
	if err != nil {
		return 0, err
	}
	return c.CreateCheckRun(ctx, repo, cr)
}

// UpdateCheck updates a check run.
func (h *Holder) UpdateCheck(ctx context.Context, installation int64, repo string, id int64, cr CheckRun) error {
	c, err := h.client(installation)
	if err != nil {
		return err
	}
	return c.UpdateCheckRun(ctx, repo, id, cr)
}

// CloneToken returns an installation token for cloning in build pods.
func (h *Holder) CloneToken(ctx context.Context, installation int64) (string, error) {
	a, err := h.Get()
	if err != nil {
		return "", err
	}
	return a.InstallationToken(ctx, installation)
}

// ChangedFiles lists files that differ between two commits (for change detection).
func (h *Holder) ChangedFiles(ctx context.Context, installation int64, repo, base, head string) ([]string, bool, error) {
	c, err := h.client(installation)
	if err != nil {
		return nil, false, err
	}
	return c.ChangedFiles(ctx, repo, base, head)
}

// BranchSHA resolves a branch to its head commit.
func (h *Holder) BranchSHA(ctx context.Context, installation int64, repo, branch string) (string, error) {
	c, err := h.client(installation)
	if err != nil {
		return "", err
	}
	return c.BranchSHA(ctx, repo, branch)
}

// ForOwner returns an API client for the installation on an account
// (user or organization), found by the account's login.
func (h *Holder) ForOwner(ctx context.Context, owner string) (*Client, int64, error) {
	app, err := h.Get()
	if err != nil {
		return nil, 0, err
	}
	insts, err := app.Installations(ctx)
	if err != nil {
		return nil, 0, err
	}
	for _, i := range insts {
		if strings.EqualFold(i.Account.Login, owner) {
			return app.Client(i.ID), i.ID, nil
		}
	}
	return nil, 0, fmt.Errorf("the GitHub App is not installed on %s", owner)
}
