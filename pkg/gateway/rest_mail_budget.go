package gateway

// rest_mail_budget.go — the A8 mail-operation budget at the REST surface:
// the four dialing Mail panel GETs (folders / list / message / attachment)
// run their IMAP dial through the shared per-account budget
// (email.MailBudget::CallValue), so an automatic panel poll during a watcher
// backoff window returns the contract's 503 MailUnavailableError without
// dialing, the human Retry click (?retry=true) bypasses ONLY the backoff
// check (the semaphore and coalescing still apply — proven at the budget
// unit), and the 2-per-account cap is shared with the agent tools and the
// watcher cycles.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// mailBudgetFor resolves the restAPI's handle on the shared A8 budget. Boot
// injects the process-wide instance at restAPI construction
// (gateway_boot.go); the lazy fallback keeps a directly constructed restAPI
// (the unit-test literal) on the SAME process-wide instance —
// email.SharedMailBudget is keyed by the state dir, so the gate is shared no
// matter how the restAPI was built.
func (a *restAPI) mailBudgetFor() *email.MailBudget {
	a.mailBudgetOnce.Do(func() {
		if a.mailBudget == nil {
			a.mailBudget = email.SharedMailBudget(a.homePath)
		}
	})
	return a.mailBudget
}

// mailRetryParam parses the ?retry= query parameter — the human Retry click.
// An unparsable value counts as false: the automatic (gated) shape.
func mailRetryParam(r *http.Request) bool {
	b, err := strconv.ParseBool(r.URL.Query().Get("retry"))
	return err == nil && b
}

// mailBudgetErr maps a budget refusal to the contract's 503
// MailUnavailableError (code backoff / busy). Returns false for other errors;
// mailBudgetWrap distinguishes invalid refs (400), missing messages (404),
// and genuine upstream failures (MC-8 502) after the dial.
func (a *restAPI) mailBudgetErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var bf *email.MailBackoffError
	if errors.As(err, &bf) {
		body := gen.MailUnavailableError{
			Code:  gen.MailUnavailableErrorCodeBackoff,
			Error: "mailbox in backoff — the watcher will retry automatically",
		}
		if bf.LastErrorClass != "" {
			cls := gen.MailUnavailableErrorLastErrorClass(bf.LastErrorClass)
			if cls.Valid() {
				body.LastErrorClass = &cls
			} else {
				// The watcher state file is untrusted content (the summary
				// endpoint renders it defensively too): an out-of-enum class
				// would be hard-rejected by the SPA's generated Zod schema —
				// breaking 503 handling entirely instead of degrading.
				// Omit the field; make the drop visible.
				slog.Warn("rest: mail budget 503 dropped out-of-enum last_error_class",
					"last_error_class", bf.LastErrorClass)
			}
		}
		if next, perr := time.Parse(time.RFC3339, bf.NextAttemptAt); perr == nil {
			body.NextAttemptAt = &next
		} else {
			// Same untrusted-content discipline: an unparsable timestamp is
			// omitted, never silently.
			slog.Warn("rest: mail budget 503 dropped unparsable next_attempt_at",
				"next_attempt_at", bf.NextAttemptAt, "error", perr)
		}
		writeJSON(w, http.StatusServiceUnavailable, body)
		return true
	}
	if errors.Is(err, email.ErrMailBusy) {
		writeJSON(w, http.StatusServiceUnavailable, gen.MailUnavailableError{
			Code:  gen.MailUnavailableErrorCodeBusy,
			Error: "mailbox busy — the request queued past its deadline under the 2-per-account cap",
		})
		return true
	}
	return false
}

// mailBudgetWrap gates one panel dial through the shared budget and maps the
// budget refusals to the contract's 503. Returns true when the response was
// fully handled (refusal or dial error); false when the dial succeeded and
// the caller should render v. Generic in the dial's result type T: the caller
// receives the value AS T — a shape drift is a compile error at the call
// site, not an unchecked runtime assertion. A package-level function (not a
// method) because Go methods cannot carry type parameters. rowsOf, when
// non-nil, derives the instrument record's optional rows member from the
// dial's value — invoked only on success (a failed operation's row count is
// unknown, and an unknown stays absent rather than degrading to a zero).
func mailBudgetWrap[T any](
	a *restAPI,
	w http.ResponseWriter,
	r *http.Request,
	agentID, workspaceID string,
	client *email.Client,
	op string,
	params map[string]any,
	dial func(context.Context) (T, error),
	rowsOf func(T) *int,
) (T, bool) {
	var zero T
	started := time.Now()
	budget := a.mailBudgetFor()
	req := email.MailBudgetRequest{
		Account:     client.AccountKey(),
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		Operation:   op,
		Params:      params,
		Retry:       mailRetryParam(r),
	}
	v, err := email.CallValue(budget, r.Context(), req, dial)
	var rows *int
	if err == nil && rowsOf != nil {
		rows = rowsOf(v)
	}
	// w5 US-7.6/MC-18: exactly one safe record per operation, success and
	// failure alike — emitted here, the single seam every panel read and
	// preview serve passes through.
	a.emitMailOperationTimingRows(op, agentID, workspaceID, started, err, "live", false, rows)
	if a.mailBudgetErr(w, err) {
		return zero, true
	}
	if err != nil {
		// Invalid refs are 400, absent messages are 404, and real upstream
		// failures after a dial retain the MC-8 502 envelope.
		switch {
		case errors.Is(err, email.ErrMailRefInvalid):
			jsonErr(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, email.ErrMessageNotFound):
			jsonErr(w, http.StatusNotFound, "message not found")
		default:
			mailErr502(w, err)
		}
		return zero, true
	}
	return v, false
}
