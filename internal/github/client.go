package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client makes API calls as one installation.
type Client struct {
	app          *App
	installation int64
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	tok, err := c.app.InstallationToken(ctx, c.installation)
	if err != nil {
		return err
	}
	return c.app.do(ctx, "token "+tok, method, path, body, out)
}

type Repo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	HTMLURL       string `json:"html_url"`
	PushedAt      string `json:"pushed_at"`
}

// Repos lists every repo the installation can access.
func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	var all []Repo
	for page := 1; ; page++ {
		var out struct {
			Repositories []Repo `json:"repositories"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Repositories...)
		if len(out.Repositories) < 100 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].PushedAt > all[j].PushedAt })
	return all, nil
}

func (c *Client) Repo(ctx context.Context, fullName string) (*Repo, error) {
	var r Repo
	return &r, c.do(ctx, http.MethodGet, "/repos/"+fullName, nil, &r)
}

// ---- repository contents as fs.FS (for detection) ----

// RepoFS exposes a commit's tree as a read-only fs.FS. Paths come from one
// recursive tree call; file contents are fetched lazily and cached, so
// detection only downloads the handful of manifests it actually reads.
type RepoFS struct {
	c     *Client
	repo  string
	files map[string]treeEntry
	dirs  map[string][]string

	mu    sync.Mutex
	cache map[string][]byte
}

type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // blob | tree
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// maxFetch bounds downloaded file size; detection reads small manifests.
const maxFetch = 1 << 20

func (c *Client) FS(ctx context.Context, repo, ref string) (*RepoFS, error) {
	var out struct {
		Tree      []treeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/trees/"+url.PathEscape(ref)+"?recursive=1", nil, &out); err != nil {
		return nil, err
	}
	r := &RepoFS{c: c, repo: repo, files: map[string]treeEntry{}, dirs: map[string][]string{".": nil}, cache: map[string][]byte{}}
	for _, e := range out.Tree {
		r.files[e.Path] = e
		dir := path.Dir(e.Path)
		r.dirs[dir] = append(r.dirs[dir], path.Base(e.Path))
		if e.Type == "tree" {
			if _, ok := r.dirs[e.Path]; !ok {
				r.dirs[e.Path] = nil
			}
		}
	}
	return r, nil
}

func (r *RepoFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if children, ok := r.dirs[name]; ok {
		return &dirFile{fs: r, name: name, children: children}, nil
	}
	e, ok := r.files[name]
	if !ok || e.Type != "blob" {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	data, err := r.blob(e)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &blobFile{info: fileInfo{name: path.Base(name), size: int64(len(data))}, r: bytes.NewReader(data)}, nil
}

func (r *RepoFS) blob(e treeEntry) ([]byte, error) {
	r.mu.Lock()
	if b, ok := r.cache[e.SHA]; ok {
		r.mu.Unlock()
		return b, nil
	}
	r.mu.Unlock()
	if e.Size > maxFetch {
		return nil, fmt.Errorf("file too large to inspect (%d bytes)", e.Size)
	}
	var out struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := r.c.do(context.Background(), http.MethodGet, "/repos/"+r.repo+"/git/blobs/"+e.SHA, nil, &out); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cache[e.SHA] = data
	r.mu.Unlock()
	return data, nil
}

type fileInfo struct {
	name string
	size int64
	dir  bool
}

func (f fileInfo) Name() string { return f.name }
func (f fileInfo) Size() int64  { return f.size }
func (f fileInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return f.dir }
func (f fileInfo) Sys() any           { return nil }

type blobFile struct {
	info fileInfo
	r    *bytes.Reader
}

func (b *blobFile) Stat() (fs.FileInfo, error) { return b.info, nil }
func (b *blobFile) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *blobFile) Close() error               { return nil }

type dirFile struct {
	fs       *RepoFS
	name     string
	children []string
	offset   int
}

func (d *dirFile) Stat() (fs.FileInfo, error) {
	return fileInfo{name: path.Base(d.name), dir: true}, nil
}
func (d *dirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: fs.ErrInvalid}
}
func (d *dirFile) Close() error { return nil }

func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	names := append([]string(nil), d.children...)
	sort.Strings(names)
	var out []fs.DirEntry
	for _, name := range names[d.offset:] {
		full := name
		if d.name != "." {
			full = d.name + "/" + name
		}
		e := d.fs.files[full]
		out = append(out, fs.FileInfoToDirEntry(fileInfo{name: name, size: e.Size, dir: e.Type == "tree"}))
		if n > 0 && len(out) == n {
			break
		}
	}
	d.offset += len(out)
	if n > 0 && len(out) == 0 {
		return nil, io.EOF
	}
	return out, nil
}

// ---- onboarding PR: one commit with every generated file ----

type PullRequest struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

// OpenPR commits files on a new branch off base in a single commit and opens
// a pull request. Re-running with the same branch replaces its commit.
func (c *Client) OpenPR(ctx context.Context, repo, base, branch, title, body string, files map[string]string) (*PullRequest, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/ref/heads/"+base, nil, &ref); err != nil {
		return nil, err
	}
	var baseCommit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/git/commits/"+ref.Object.SHA, nil, &baseCommit); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	type entry struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	var entries []entry
	for _, p := range paths {
		entries = append(entries, entry{Path: p, Mode: "100644", Type: "blob", Content: files[p]})
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/git/trees", map[string]any{"base_tree": baseCommit.Tree.SHA, "tree": entries}, &tree); err != nil {
		return nil, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/git/commits", map[string]any{
		"message": title, "tree": tree.SHA, "parents": []string{ref.Object.SHA},
	}, &commit); err != nil {
		return nil, err
	}
	err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/git/refs", map[string]any{"ref": "refs/heads/" + branch, "sha": commit.SHA}, nil)
	var ae *APIError
	if errorsAs(err, &ae) && ae.Status == http.StatusUnprocessableEntity {
		err = c.do(ctx, http.MethodPatch, "/repos/"+repo+"/git/refs/heads/"+branch, map[string]any{"sha": commit.SHA, "force": true}, nil)
	}
	if err != nil {
		return nil, err
	}
	var pr PullRequest
	err = c.do(ctx, http.MethodPost, "/repos/"+repo+"/pulls", map[string]any{"title": title, "head": branch, "base": base, "body": body}, &pr)
	if errorsAs(err, &ae) && ae.Status == http.StatusUnprocessableEntity {
		// A PR for this branch already exists; return it.
		var prs []PullRequest
		owner := strings.Split(repo, "/")[0]
		if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/pulls?state=open&head="+url.QueryEscape(owner+":"+branch), nil, &prs); err != nil {
			return nil, err
		}
		if len(prs) > 0 {
			return &prs[0], nil
		}
	}
	return &pr, err
}

// ---- check runs ----

type CheckRun struct {
	Name       string `json:"name,omitempty"`
	HeadSHA    string `json:"head_sha,omitempty"`
	Status     string `json:"status,omitempty"`     // queued | in_progress | completed
	Conclusion string `json:"conclusion,omitempty"` // success | failure | cancelled
	DetailsURL string `json:"details_url,omitempty"`
	Output     *struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output,omitempty"`
}

func CheckOutput(title, summary string) *struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
} {
	return &struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	}{title, summary}
}

func (c *Client) CreateCheckRun(ctx context.Context, repo string, cr CheckRun) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	return out.ID, c.do(ctx, http.MethodPost, "/repos/"+repo+"/check-runs", cr, &out)
}

func (c *Client) UpdateCheckRun(ctx context.Context, repo string, id int64, cr CheckRun) error {
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/check-runs/%d", repo, id), cr, nil)
}

// FileAt returns a file at a ref, or IsNotFound.
func (c *Client) FileAt(ctx context.Context, repo, filePath, ref string) ([]byte, error) {
	var out struct {
		Content string `json:"content"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/contents/"+filePath+"?ref="+url.QueryEscape(ref), nil, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
}

// ChangedFiles lists files that differ between two commits. complete is
// false when GitHub truncated the list (it returns at most 300 files).
func (c *Client) ChangedFiles(ctx context.Context, repo, base, head string) (files []string, complete bool, err error) {
	var out struct {
		Files []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
		} `json:"files"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/compare/"+url.PathEscape(base)+"..."+url.PathEscape(head)+"?per_page=300", nil, &out); err != nil {
		return nil, false, err
	}
	for _, f := range out.Files {
		files = append(files, f.Filename)
		if f.PreviousFilename != "" {
			files = append(files, f.PreviousFilename) // a move affects both folders
		}
	}
	return files, len(out.Files) < 300, nil
}

// BranchSHA returns the commit a branch points at.
func (c *Client) BranchSHA(ctx context.Context, repo, branch string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/commits/"+url.PathEscape(branch), nil, &out); err != nil {
		return "", err
	}
	return out.SHA, nil
}

// PutFile creates or updates one file on a branch as a single commit and
// returns the commit's SHA.
func (c *Client) PutFile(ctx context.Context, repo, branch, filePath, content, message string) (string, error) {
	body := map[string]any{"message": message, "content": base64.StdEncoding.EncodeToString([]byte(content)), "branch": branch}
	if sha, err := c.fileSHA(ctx, repo, branch, filePath); err == nil {
		body["sha"] = sha // updating: GitHub needs the current blob
	} else if !IsNotFound(err) {
		return "", err
	}
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	err := c.do(ctx, http.MethodPut, "/repos/"+repo+"/contents/"+filePath, body, &out)
	return out.Commit.SHA, err
}

// DeleteFile removes one file on a branch as a single commit.
func (c *Client) DeleteFile(ctx context.Context, repo, branch, filePath, message string) (string, error) {
	sha, err := c.fileSHA(ctx, repo, branch, filePath)
	if err != nil {
		return "", err
	}
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	err = c.do(ctx, http.MethodDelete, "/repos/"+repo+"/contents/"+filePath, map[string]any{"message": message, "sha": sha, "branch": branch}, &out)
	return out.Commit.SHA, err
}

func (c *Client) fileSHA(ctx context.Context, repo, branch, filePath string) (string, error) {
	var out struct {
		SHA string `json:"sha"`
	}
	err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/contents/"+filePath+"?ref="+url.QueryEscape(branch), nil, &out)
	return out.SHA, err
}
