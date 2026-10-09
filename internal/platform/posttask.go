package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"

	. "github.com/p0dxD/rendimiento.ai/internal/i18n" //nolint:revive // M marks messages for people
)

// Post-deploy tasks run against a release once it is live: a Kubernetes
// Job in the app's namespace, so they reach its services and databases by
// name. With service:, the task runs in that service's live image with its
// environment (copied from the Deployment the controller applied for this
// release): what a migration needs.

// defaultTaskTimeout bounds a post-deploy task without its own timeout.
const defaultTaskTimeout = 10 * time.Minute

// stageTasks are the release's tasks of one stage (pre- or post-deploy).
func stageTasks(rel *store.Release, stage string) []spec.Task {
	var out []spec.Task
	for _, t := range rel.Spec.Tasks {
		if t.Stage == stage {
			out = append(out, t)
		}
	}
	return out
}

// postDeployTasks are the release's post-deploy tasks.
func postDeployTasks(rel *store.Release) []spec.Task { return stageTasks(rel, spec.StagePostDeploy) }

// runPostDeploy runs a release's post-deploy tasks (see runStage).
func (p *Platform) runPostDeploy(ctx context.Context, app *store.App, rel *store.Release) []store.ReleaseTask {
	return p.runStage(ctx, app, rel, spec.StagePostDeploy)
}

// runStage runs a release's tasks of one stage, each once its after: tasks
// succeeded (a task whose dependency failed is skipped), and records each
// one's state as it goes.
func (p *Platform) runStage(ctx context.Context, app *store.App, rel *store.Release, stage string) []store.ReleaseTask {
	tasks := stageTasks(rel, stage)
	run := p.Verify.RunTask
	if run == nil {
		run = p.runTaskJob
	}
	results := map[string]store.ReleaseTask{}
	done := map[string]bool{}
	for len(done) < len(tasks) && ctx.Err() == nil {
		// The tasks whose dependencies have all finished run together.
		var ready []spec.Task
		for _, t := range tasks {
			if done[t.Name] {
				continue
			}
			waiting := false
			for _, a := range t.After {
				waiting = waiting || !done[a]
			}
			if !waiting {
				ready = append(ready, t)
			}
		}
		if len(ready) == 0 {
			break // a cycle; validation prevents it
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, t := range ready {
			blocked := ""
			for _, a := range t.After {
				if results[a].Status != store.TaskSucceeded {
					blocked = a
				}
			}
			if blocked != "" {
				r := store.ReleaseTask{ReleaseID: rel.ID, Name: t.Name, Stage: stage, Optional: t.Optional, Status: store.TaskSkipped, Message: M("%s did not succeed", blocked)}
				p.saveTask(r)
				results[t.Name], done[t.Name] = r, true
				continue
			}
			wg.Add(1)
			go func(t spec.Task) {
				defer wg.Done()
				now := time.Now()
				p.saveTask(store.ReleaseTask{ReleaseID: rel.ID, Name: t.Name, Stage: stage, Optional: t.Optional, Status: store.TaskRunning, StartedAt: &now})
				r := run(ctx, app, rel, t)
				r.ReleaseID, r.Name, r.Stage, r.Optional, r.StartedAt = rel.ID, t.Name, stage, t.Optional, &now
				if r.FinishedAt == nil {
					end := time.Now()
					r.FinishedAt = &end
				}
				p.saveTask(r)
				mu.Lock()
				results[t.Name] = r
				mu.Unlock()
			}(t)
		}
		wg.Wait()
		for _, t := range ready {
			done[t.Name] = true
		}
	}
	out := make([]store.ReleaseTask, 0, len(results))
	for _, t := range tasks {
		if r, ok := results[t.Name]; ok {
			out = append(out, r)
		}
	}
	return out
}

// saveTask records a deploy task's state; a failure to record is only
// logged.
func (p *Platform) saveTask(t store.ReleaseTask) {
	if err := p.Store.SaveReleaseTask(context.Background(), t); err != nil {
		p.Log.Warn("could not record a post-deploy task", "task", t.Name, "err", err)
	}
}

// judgeTasks folds post-deploy results into a verification: a failed
// required task fails the release; a failed optional one is a warning.
func judgeTasks(results []store.ReleaseTask) (failures, warnings []string) {
	for _, r := range results {
		if r.Status == store.TaskSucceeded {
			continue
		}
		stage := r.Stage
		if stage == "" {
			stage = spec.StagePostDeploy
		}
		line := M("%s task %s %s", stage, r.Name, r.Status)
		if r.Message != "" {
			line = M("%s task %s %s: %s", stage, r.Name, r.Status, r.Message)
		}
		if r.Optional {
			warnings = append(warnings, M("%s (optional)", line))
		} else {
			failures = append(failures, line)
		}
	}
	return failures, warnings
}

var nonJobName = regexp.MustCompile(`[^a-z0-9-]+`)

// taskJob builds the Job for a post-deploy task. base, for a service: task,
// is the service's live container: its image, env and mounts are reused.
func taskJob(app string, rel *store.Release, t spec.Task, base *corev1.Container, baseVolumes []corev1.Volume, suffix string) *batchv1.Job {
	prefix := "post"
	if t.PreDeploy() {
		prefix = "pre"
	}
	name := nonJobName.ReplaceAllString(strings.ToLower(fmt.Sprintf("%s-%s-r%d", prefix, t.Name, rel.Number)), "-")
	if len(name) > 52 {
		name = name[:52]
	}
	name = strings.TrimRight(name, "-") + "-" + suffix
	timeout := defaultTaskTimeout
	if t.Timeout > 0 {
		timeout = time.Duration(t.Timeout) * time.Second
	}
	deadline := int64(timeout.Seconds())
	labels := map[string]string{
		render.LabelManagedBy: render.ManagedBy, render.LabelApp: app,
		"rendimiento.ai/task":    nonJobName.ReplaceAllString(t.Name, "-"),
		"rendimiento.ai/stage":   t.Stage,
		"rendimiento.ai/release": fmt.Sprint(rel.Number),
	}
	c := corev1.Container{Name: "task", Image: t.Image, Command: []string{"sh", "-c", t.Command}}
	var volumes []corev1.Volume
	if base != nil {
		c.Image, c.Env, c.EnvFrom = base.Image, append([]corev1.EnvVar(nil), base.Env...), append([]corev1.EnvFromSource(nil), base.EnvFrom...)
		// Before the rollout the live Deployment still runs the previous
		// release: keep its environment, use the new release's image.
		if img := rel.Images[t.Service]; t.PreDeploy() && img != "" {
			c.Image = img
		}
		// Secret and ConfigMap files come along; data volumes do not (a
		// ReadWriteOnce claim is the running pod's).
		keep := map[string]bool{}
		for _, v := range baseVolumes {
			if v.Secret != nil || v.ConfigMap != nil {
				volumes = append(volumes, v)
				keep[v.Name] = true
			}
		}
		for _, m := range base.VolumeMounts {
			if keep[m.Name] {
				c.VolumeMounts = append(c.VolumeMounts, m)
			}
		}
	}
	c.Env = append(c.Env,
		corev1.EnvVar{Name: "RENDIMIENTO_APP", Value: app},
		corev1.EnvVar{Name: "RENDIMIENTO_RELEASE", Value: fmt.Sprint(rel.Number)},
		corev1.EnvVar{Name: "GIT_SHA", Value: rel.SHA},
	)
	keys := make([]string, 0, len(t.Env))
	for k := range t.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.Env = append(c.Env, corev1.EnvVar{Name: k, Value: t.Env[k]})
	}
	for _, sec := range t.Secrets {
		c.EnvFrom = append(c.EnvFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: sec}}})
	}
	keys = keys[:0]
	for k := range t.SecretEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		name, key, _ := strings.Cut(t.SecretEnv[k], "/")
		c.Env = append(c.Env, corev1.EnvVar{Name: k, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}})
	}
	res := spec.ResourcesFor(t.Size, t.Resources)
	c.Resources = corev1.ResourceRequirements{Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{}}
	for _, q := range []struct {
		list  corev1.ResourceList
		name  corev1.ResourceName
		value string
	}{{c.Resources.Requests, corev1.ResourceCPU, res.CPURequest}, {c.Resources.Requests, corev1.ResourceMemory, res.MemRequest},
		{c.Resources.Limits, corev1.ResourceCPU, res.CPULimit}, {c.Resources.Limits, corev1.ResourceMemory, res.MemLimit}} {
		if v, err := resource.ParseQuantity(q.value); err == nil && q.value != "" {
			q.list[q.name] = v
		}
	}
	zero, ttl := int32(0), int32(24*3600)
	off := false
	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: app, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit: &zero, ActiveDeadlineSeconds: &deadline, TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever, AutomountServiceAccountToken: &off,
					Containers: []corev1.Container{c}, Volumes: volumes,
				},
			},
		},
	}
}

// runTaskJob runs one post-deploy task as a Job and waits for it, keeping
// its log.
func (p *Platform) runTaskJob(ctx context.Context, app *store.App, rel *store.Release, t spec.Task) store.ReleaseTask {
	fail := func(format string, a ...any) store.ReleaseTask {
		return store.ReleaseTask{Status: store.TaskFailed, Message: M(format, a...)}
	}
	if p.Clientset == nil {
		return fail("post-deploy tasks need the platform's Kubernetes clientset")
	}
	var base *corev1.Container
	var baseVolumes []corev1.Volume
	if t.Service != "" {
		dep, err := p.Clientset.AppsV1().Deployments(app.Name).Get(ctx, t.Service, metav1.GetOptions{})
		if err != nil || len(dep.Spec.Template.Spec.Containers) == 0 {
			return fail("could not read the %s deployment to copy its image and environment: %v", t.Service, err)
		}
		base, baseVolumes = &dep.Spec.Template.Spec.Containers[0], dep.Spec.Template.Spec.Volumes
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	job := taskJob(app.Name, rel, t, base, baseVolumes, hex.EncodeToString(b)[:5])
	jobs := p.Clientset.BatchV1().Jobs(app.Name)
	if _, err := jobs.Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fail("could not create the job: %v", err)
	}
	timeout := time.Duration(*job.Spec.ActiveDeadlineSeconds)*time.Second + time.Minute
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var logBuf bytes.Buffer
	var logMu sync.Mutex
	logDone := make(chan struct{})
	go func() { // follow the pod's log as it runs
		defer close(logDone)
		pod := p.waitTaskPod(wctx, app.Name, job.Name)
		if pod == "" {
			return
		}
		rc, err := p.Clientset.CoreV1().Pods(app.Name).GetLogs(pod, &corev1.PodLogOptions{Follow: true}).Stream(wctx)
		if err != nil {
			return
		}
		defer rc.Close()
		_, _ = io.Copy(&lockedWriter{w: &logBuf, mu: &logMu}, rc)
	}()

	result := store.ReleaseTask{Status: store.TaskFailed, Message: M("timed out")}
	for wctx.Err() == nil {
		j, err := jobs.Get(wctx, job.Name, metav1.GetOptions{})
		if err == nil {
			if j.Status.Succeeded > 0 {
				result = store.ReleaseTask{Status: store.TaskSucceeded}
				break
			}
			if j.Status.Failed > 0 || jobFailed(j) {
				result = store.ReleaseTask{Status: store.TaskFailed, Message: jobFailure(j)}
				break
			}
		}
		select {
		case <-wctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
	select { // let the log stream catch up, briefly
	case <-logDone:
	case <-time.After(5 * time.Second):
	}
	logMu.Lock()
	result.Log = logBuf.String()
	logMu.Unlock()
	if result.Status == store.TaskFailed {
		if tail := lastLines(result.Log, 3); tail != "" {
			result.Message += " · " + tail
		}
	}
	end := time.Now()
	result.FinishedAt = &end
	return result
}

// waitTaskPod returns the name of a Job's pod once it has started (or
// finished), or "" if it never does.
func (p *Platform) waitTaskPod(ctx context.Context, ns, job string) string {
	for ctx.Err() == nil {
		pods, err := p.Clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + job})
		if err == nil {
			for _, pod := range pods.Items {
				for _, cs := range pod.Status.ContainerStatuses {
					if cs.State.Running != nil || cs.State.Terminated != nil {
						return pod.Name
					}
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	return ""
}

// jobFailed reports whether Kubernetes marked the task's Job failed.
func jobFailed(j *batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// jobFailure says why the Job failed ("timed out", or its reason).
func jobFailure(j *batchv1.Job) string {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			if c.Reason == "DeadlineExceeded" {
				return M("timed out")
			}
			return strings.TrimSpace(c.Reason + " " + c.Message)
		}
	}
	return M("the task failed")
}

// lastLines is the last n lines of a log on one line, to quote in a message.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, " ⏎ "))
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

// Write appends b under the lock (the log stream and the reader share the buffer).
func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}
