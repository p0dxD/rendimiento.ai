# Your environment

rendimiento is general, but it runs somewhere specific: a six-node k3s cluster of Raspberry Pis and a GPU node on a home network. This part of the book describes that environment and how to keep it running.

- **[The cluster](cluster.md)**: nodes, network and IP addresses, namespaces, and the building blocks rendimiento relies on (ingress, certificates, storage, load balancer, registry, BuildKit, DNS, GPU).
- **[How the platform is deployed](deploy.md)**: the `deploy/` manifests, the secrets to create, the GitHub App, and how a new version of rendimiento gets onto the cluster.
- **[Runbooks](runbooks.md)**: step-by-step fixes for the things that have gone wrong before, and routine operations.
