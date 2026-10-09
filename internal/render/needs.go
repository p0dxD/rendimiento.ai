package render

import (
	"fmt"
	"sort"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// Secrets rendimiento creates (once, with generated passwords) for needs.
const (
	PostgresSecret = "postgres-credentials"
	RedisSecret    = "redis-credentials"
)

// NeedServices are the services rendimiento runs for the app's needs: one
// Postgres and one Redis per app, shared by every service that needs them.
// They are ordinary ready-made-image services, so they get the same
// deployment, volume, health and security handling as the app's own.
func NeedServices(s spec.Spec) []spec.Service {
	var out []spec.Service
	if s.Needs(spec.NeedPostgres) {
		o := spec.PostgresOptions{}
		if s.Postgres != nil {
			o = *s.Postgres
		}
		version, size := orDefault(o.Version, "17"), orDefault(o.Size, "5Gi")
		res := &spec.ResourceOverride{CPU: "100m", Memory: "256Mi", CPULimit: "none", MemoryLimit: "512Mi"}
		if o.Resources != nil {
			res = o.Resources
		}
		out = append(out, spec.Service{
			Name: spec.NeedPostgres, Image: "postgres:" + version + "-alpine", Port: 5432, Replicas: 1, Size: spec.SizeSmall,
			Resources: res, Health: &spec.Health{TCP: true},
			Volume: &spec.Volume{Size: size, Mount: "/var/lib/postgresql/data"},
			Env:    map[string]string{"PGDATA": "/var/lib/postgresql/data/pgdata"},
			SecretEnv: map[string]string{
				"POSTGRES_USER": PostgresSecret + "/username", "POSTGRES_PASSWORD": PostgresSecret + "/password", "POSTGRES_DB": PostgresSecret + "/database",
			},
		})
	}
	if s.Needs(spec.NeedRedis) {
		o := spec.RedisOptions{}
		if s.Redis != nil {
			o = *s.Redis
		}
		res := &spec.ResourceOverride{CPU: "50m", Memory: "64Mi", CPULimit: "none", MemoryLimit: "256Mi"}
		if o.Resources != nil {
			res = o.Resources
		}
		out = append(out, spec.Service{
			Name: spec.NeedRedis, Image: "redis:" + orDefault(o.Version, "7") + "-alpine", Port: 6379, Replicas: 1, Size: spec.SizeSmall,
			Resources: res, Health: &spec.Health{TCP: true},
			// A cache: no persistence, oldest keys evicted when full.
			Command: []string{"redis-server"},
			Args: []string{"--requirepass", "$(REDIS_PASSWORD)", "--maxmemory", orDefault(o.MaxMemory, "64mb"),
				"--maxmemory-policy", "allkeys-lru", "--save", "", "--appendonly", "no"},
			SecretEnv: map[string]string{"REDIS_PASSWORD": RedisSecret + "/password"},
		})
	}
	return out
}

// needKind is name when it is a database or cache this app runs (postgres,
// redis), else "".
func needKind(name string, s spec.Spec) string {
	if (name == spec.NeedPostgres || name == spec.NeedRedis) && s.Needs(name) {
		return name
	}
	return ""
}

// orDefault is v, or def when v is "".
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// withNeeds adds the need services and, to every service that needs
// something, the variables to reach it. The app's own env wins.
func withNeeds(in Input) ([]spec.Service, error) {
	var out []spec.Service
	for _, svc := range in.Spec.Services {
		if len(svc.Needs) == 0 {
			out = append(out, svc)
			continue
		}
		env := copyMap(svc.Env)
		secretEnv := copyMap(svc.SecretEnv)
		set := func(m map[string]string, k, v string) {
			if _, own := svc.Env[k]; own {
				return
			}
			if _, own := svc.SecretEnv[k]; own {
				return
			}
			m[k] = v
		}
		for _, n := range svc.Needs {
			switch n.Kind {
			case spec.NeedPostgres:
				set(secretEnv, n.EnvName(), PostgresSecret+"/uri")
				// The standard libpq variables, for tools and drivers that read them.
				for k, key := range map[string]string{"PGHOST": "host", "PGPORT": "port", "PGUSER": "username", "PGPASSWORD": "password", "PGDATABASE": "database"} {
					set(secretEnv, k, PostgresSecret+"/"+key)
				}
			case spec.NeedRedis:
				set(secretEnv, n.EnvName(), RedisSecret+"/uri")
			case spec.NeedService:
				url, ok := in.ServiceURLs[n.Service]
				if !ok {
					return nil, fmt.Errorf("service %s needs %s, which was not found", svc.Name, n.Service)
				}
				set(env, n.EnvName(), url)
			}
		}
		svc.Env, svc.SecretEnv = env, secretEnv
		out = append(out, svc)
	}
	return append(out, NeedServices(in.Spec)...), nil
}

// copyMap is a copy of m.
func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// NeedRefs lists the other services an app's services need, as written
// ("namespace/name" or a same-app "name"), sorted and unique.
func NeedRefs(s spec.Spec) []string {
	seen := map[string]bool{}
	var out []string
	for _, svc := range s.Services {
		for _, n := range svc.Needs {
			if n.Kind == spec.NeedService && !seen[n.Service] {
				seen[n.Service] = true
				out = append(out, n.Service)
			}
		}
	}
	sort.Strings(out)
	return out
}
