// Package testenv is the envtest (a real API server and etcd) the
// controller and platform tests run against, tuned for slow machines.
package testenv

import "sigs.k8s.io/controller-runtime/pkg/envtest"

// New is an envtest with the CRDs in crdDirs. Its etcd waits longer before
// giving up on a write: by default a write not applied within 7 seconds
// fails with "etcdserver: request timed out", which happened on the
// cluster's Raspberry Pis while two test API servers ran at once. With a
// 5-second election timeout the limit is 15 seconds (5 + 2 × 5); one etcd
// never holds an election anyway.
func New(crdDirs ...string) *envtest.Environment {
	etcd := &envtest.Etcd{}
	etcd.Configure().
		Set("election-timeout", "5000").
		Set("heartbeat-interval", "500")
	return &envtest.Environment{
		CRDDirectoryPaths:     crdDirs,
		ErrorIfCRDPathMissing: true,
		ControlPlane:          envtest.ControlPlane{Etcd: etcd},
	}
}
