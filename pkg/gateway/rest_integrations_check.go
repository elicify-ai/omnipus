package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const searchConnectionCheckDeadline = 15 * time.Second
const searchConnectionCheckCooldown = 30 * time.Second

func (a *restAPI) handleSearchConnectionCheck(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	const prefix = "/api/v1/integrations/providers/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/check") {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/check")
	def, known := integrationDefByID(id)
	if !known {
		jsonErr(w, http.StatusNotFound, "unknown integration provider")
		return
	}
	if def.kind != "search" || !def.requiresKey {
		jsonErr(w, http.StatusBadRequest, "only keyed search services support a connection check")
		return
	}
	var body gen.SearchProviderCheckRequest
	if !decodeAndValidate(w, r, "SearchProviderCheckRequest", &body, true) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), searchConnectionCheckDeadline)
	defer cancel()
	probe, retry, err := a.admitSearchConnectionCheck(ctx, id)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// Use the same closed outcomes as an interrupted outbound check;
			// cancellation does not introduce a new wire status.
			status := gen.SearchProviderCheckResponseStatusNetworkError
			if errors.Is(err, context.DeadlineExceeded) {
				status = gen.SearchProviderCheckResponseStatusTimeout
			}
			jsonOK(w, gen.SearchProviderCheckResponse{
				ProviderId: gen.SearchProviderCheckResponseProviderId(id),
				Status:     status,
				CheckedAt:  time.Now().UTC(),
			})
			return
		}
		if retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
		}
		writeIntegrationChangeError(w, err, "Could not prepare the connection check. Try again.")
		return
	}
	defer a.searchChecks.finish(id)
	response, err := probe.Check(ctx)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "The connection check could not run. Try again.")
		return
	}
	jsonOK(w, response)
}

// admitSearchConnectionCheck serializes key/config snapshots and admission with
// integration saves/removals and the loop's sysagent config writers. The locks
// are released before any outbound request, so removing a key need not wait for
// an already-sent diagnostic; its pinned response contains no credential data.
func (a *restAPI) admitSearchConnectionCheck(ctx context.Context, id string) (*tools.SearchConnectionProbe, int, error) {
	if err := lockSearchCheckMutex(ctx, &a.configMu); err != nil {
		return nil, 0, err
	}
	defer a.configMu.Unlock()
	var probe *tools.SearchConnectionProbe
	var retry int
	err := a.agentLoop.WithConfigReadLockContext(ctx, func(cfg *config.Config) error {
		if cfg == nil {
			return &integrationChangeError{http.StatusServiceUnavailable, "The current configuration is unavailable."}
		}
		def, known := config.SearchProviderDefByID(id)
		if !known || !def.Keyed {
			return &integrationChangeError{http.StatusBadRequest, "only keyed search services support a connection check"}
		}
		ref := def.APIKeyRef(&cfg.Tools.Web)
		if ref == "" || !cfg.Tools.Web.UsableSearchProvider(id) {
			return &integrationChangeError{http.StatusConflict, "Save a key and switch this service on before checking the connection."}
		}
		key, err := a.savedIntegrationCredential(ref)
		if err != nil {
			var missing *credentials.NotFoundError
			if errors.As(err, &missing) {
				return &integrationChangeError{http.StatusConflict, "Save a key before checking the connection."}
			}
			return &integrationChangeError{http.StatusServiceUnavailable, "Could not read the saved key from the credential store. Unlock or repair it, then try again."}
		}
		if strings.TrimSpace(key) == "" || def.APIKey(&cfg.Tools.Web) != key {
			return &integrationChangeError{http.StatusConflict, "The saved key is not available to the running service. Reload the configuration, then try again."}
		}
		probe, err = tools.NewSearchConnectionProbe(id, key, cfg.Tools.Web, cfg.Context.IngestBoundBytes, a.ssrfChecker)
		if err != nil {
			return &integrationChangeError{http.StatusInternalServerError, "Could not prepare the search client. Check its configuration, then try again."}
		}
		var admitted bool
		retry, admitted, err = a.searchChecks.begin(ctx, id)
		if err != nil {
			return err
		}
		if !admitted {
			return &integrationChangeError{http.StatusTooManyRequests, "A connection check is already running or was started recently. Wait before trying again."}
		}
		return nil
	})
	return probe, retry, err
}

// lockSearchCheckMutex waits without leaving a goroutine that could acquire the
// mutex after cancellation. On success the caller owns mu and must unlock it.
func lockSearchCheckMutex(ctx context.Context, mu *sync.Mutex) error {
	retry := time.NewTicker(10 * time.Millisecond)
	defer retry.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mu.TryLock() {
			if err := ctx.Err(); err != nil {
				mu.Unlock()
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retry.C:
		}
	}
}

// A slot exists only for each static catalogue entry, never an account, URL,
// caller-supplied string or key generation. Zero value is ready for use.
type searchCheckAdmission struct {
	mu    sync.Mutex
	slots []searchCheckSlot
}

type searchCheckSlot struct {
	inFlight      bool
	nextAdmission time.Time
}

func (g *searchCheckAdmission) begin(ctx context.Context, id string) (int, bool, error) {
	if err := lockSearchCheckMutex(ctx, &g.mu); err != nil {
		return 0, false, err
	}
	defer g.mu.Unlock()
	if g.slots == nil {
		g.slots = make([]searchCheckSlot, len(config.SearchProviderCatalogue))
	}
	index := searchCheckCatalogueIndex(id)
	if index < 0 || index >= len(g.slots) {
		return 1, false, nil
	}
	slot := &g.slots[index]
	now := time.Now()
	if slot.inFlight || now.Before(slot.nextAdmission) {
		seconds := max(1, int((slot.nextAdmission.Sub(now)+time.Second-1)/time.Second))
		return seconds, false, nil
	}
	// Check under the admission mutex immediately before spending the pause,
	// including cancellation during credential resolution or client creation.
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	slot.inFlight = true
	slot.nextAdmission = now.Add(searchConnectionCheckCooldown)
	return 0, true, nil
}

func (g *searchCheckAdmission) finish(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	index := searchCheckCatalogueIndex(id)
	if index >= 0 && index < len(g.slots) {
		g.slots[index].inFlight = false
	}
}

func searchCheckCatalogueIndex(id string) int {
	for index, def := range config.SearchProviderCatalogue {
		if def.ID == id {
			return index
		}
	}
	return -1
}
