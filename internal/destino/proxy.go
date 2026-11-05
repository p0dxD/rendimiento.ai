package destino

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Proxy sends each request to the app running at its host on this computer
// (<app>.localhost, <service>.<app>.localhost, or a service's domain), as
// the cluster's ingress does.
func (l *Local) Proxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr := l.Ruta(r.Host)
		if addr == "" {
			http.Error(w, "nothing runs at "+r.Host+" on this computer", http.StatusNotFound)
			return
		}
		p := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: addr})
		p.FlushInterval = -1 // streams (server-sent events) go through as they come
		p.ServeHTTP(w, r)
	})
}
