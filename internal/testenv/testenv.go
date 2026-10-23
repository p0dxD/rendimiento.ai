// Package testenv is the envtest (a real API server and etcd) the
// controller and platform tests run against, tuned for slow machines.
package testenv

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// New is an envtest with the CRDs in crdDirs. Its etcd waits longer before
// giving up on a write: by default a write not applied within 7 seconds
// fails with "etcdserver: request timed out", which happened on the
// cluster's Raspberry Pis while two test API servers ran at once. With a
// 5-second election timeout the limit is 15 seconds (5 + 2 × 5); one etcd
// never holds an election anyway.
//
// Even so, writes still timed out now and then (runs 612 and 681): the
// wait was etcd's fsync on a busy worker's disk. A test's etcd is thrown
// away at the end, so it does not sync to disk at all (unsafe-no-fsync):
// writes no longer wait for the disk.
//
// Starting also gets longer than envtest's 20 seconds: with several builds
// on the same worker, etcd sometimes took longer than that to answer and the
// test failed with "timeout waiting for process etcd to start".
func New(crdDirs ...string) *envtest.Environment {
	etcd := &envtest.Etcd{}
	etcd.Configure().
		Set("election-timeout", "5000").
		Set("heartbeat-interval", "500").
		Set("unsafe-no-fsync", "true")
	return &envtest.Environment{
		CRDDirectoryPaths:     crdDirs,
		ErrorIfCRDPathMissing: true,
		ControlPlane:          envtest.ControlPlane{Etcd: etcd},
		// etcd and the API server. Not the stop timeout: Stop waits all of it
		// (60 s made every test 40 s slower and the package hit go test's 10 minutes).
		ControlPlaneStartTimeout: 90 * time.Second,
	}
}
