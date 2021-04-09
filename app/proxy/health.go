package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/umputun/reproxy/app/discovery"
)

func (h *Http) healthMiddleware(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(strings.ToLower(r.URL.Path), "/health") {
			h.healthHandler(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func (h *Http) healthHandler(w http.ResponseWriter, r *http.Request) {

	// runs pings in parallel
	check := func(mappers []discovery.UrlMapper) (ok bool, valid int, total int, errs []string) {
		outCh := make(chan error, 8)
		var pinged int32
		var wg sync.WaitGroup
		for _, m := range mappers {
			if m.PingURL == "" {
				continue
			}
			wg.Add(1)
			go func(m discovery.UrlMapper) {
				defer wg.Done()

				atomic.AddInt32(&pinged, 1)
				client := http.Client{Timeout: 100 * time.Millisecond}
				resp, err := client.Get(m.PingURL)
				if err != nil {
					log.Printf("[WARN] failed to ping for health %s, %v", m.PingURL, err)
					outCh <- fmt.Errorf("%s, %v", m.PingURL, err)
					return
				}
				if resp.StatusCode != http.StatusOK {
					log.Printf("[WARN] failed ping status for health %s (%s)", m.PingURL, resp.Status)
					outCh <- fmt.Errorf("%s, %s", m.PingURL, resp.Status)
					return
				}
			}(m)
		}

		go func() {
			wg.Wait()
			close(outCh)
		}()

		for e := range outCh {
			errs = append(errs, e.Error())
		}
		return len(errs) == 0, int(atomic.LoadInt32(&pinged)) - len(errs), len(mappers), errs
	}

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	ok, valid, total, errs := check(h.Mappers())
	if !ok {
		w.WriteHeader(http.StatusExpectationFailed)
		_, err := fmt.Fprintf(w, `{"status": "failed", "passed": %d, "failed":%d, "errors": "%+v"}`, valid, total-valid, errs)
		if err != nil {
			log.Printf("[WARN] failed %v", err)
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintf(w, `{"status": "ok", "services": %d}`, valid)
	if err != nil {
		log.Printf("[WARN] failed to send halth, %v", err)
	}
}